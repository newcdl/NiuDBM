package httpapi

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"niudbm/server/internal/store"
)

// 实例占用的端口池：33000 起，避开 NAS 上常见的 3306/3307。
const (
	portPoolMin = 33000
	portPoolMax = 33999
)

// instanceDir 是实例在应用数据目录下的私有目录。
//
// 台账里 DataDir 记的是 "<id>/mysql"，这里取它的父目录：
// 实例的配置、密码、部署日志都放在一起，删除实例时整目录归档/清理。
func (s *Server) instanceDir(in store.Instance) string {
	rel := filepath.FromSlash(in.DataDir)
	if rel == "" {
		rel = in.ID
	}
	return filepath.Join(s.paths.Instances, filepath.Dir(rel))
}

// storageDir 返回实例数据在宿主机上的真实目录。
//
// 绑挂方式：用户指定了路径就用它，没指定就落在实例目录的 data 子目录——
// 这样"删除服务并清除数据"能一并清掉，用户也找得到文件。
// 命名卷方式：数据由 docker 管，这里返回空串，由 storageLabel 给出可读位置。
func (s *Server) storageDir(in store.Instance) string {
	if isExternal(in) {
		return ""
	}
	if in.StorageKind == storageVolume {
		return ""
	}
	if in.StoragePath != "" {
		return filepath.FromSlash(in.StoragePath)
	}
	return filepath.Join(s.instanceDir(in), "data")
}

// storageLabel 是给用户看的数据位置：目录路径，或命名卷的名字。
func (s *Server) storageLabel(in store.Instance) string {
	if isExternal(in) {
		return in.Host
	}
	if in.StorageKind == storageVolume {
		return "docker 卷 " + volumeName(in.ID)
	}
	return s.storageDir(in)
}

// ---------- 密码落盘 ----------
//
// 密码单独存文件（0600），不写进台账：台账会被导出、备份、贴进工单，
// 而密码只应该留在数据目录里。

func (s *Server) secretPath(id string) string {
	return filepath.Join(s.paths.Instances, id, "secret")
}

func (s *Server) rootSecretPath(id string) string {
	return filepath.Join(s.paths.Instances, id, "root-secret")
}

// externalSecretPath 保存外部数据库的管理密码。
// 外部连接没有实例数据目录，凭据放在 etc/secrets 下，不进入共享备份目录。
func (s *Server) externalSecretPath(id string) string {
	return filepath.Join(s.paths.Secrets, id+".secret")
}

// writeSecret 保存管理账号（niudbm）密码。
func (s *Server) writeSecret(id, password string) error {
	return writeSecretFile(s.secretPath(id), password)
}

// writeRootSecret 保存容器 root 密码。
func (s *Server) writeRootSecret(id, password string) error {
	return writeSecretFile(s.rootSecretPath(id), password)
}

// readSecret 读管理账号密码；文件不存在时返回空串（调用方会得到认证失败，而不是崩溃）。
func (s *Server) readSecret(id string) string { return readSecretFile(s.secretPath(id)) }

// readRootSecret 读 root 密码。
func (s *Server) readRootSecret(id string) string { return readSecretFile(s.rootSecretPath(id)) }

func (s *Server) writeExternalSecret(id, password string) error {
	key, err := s.externalEncryptionKey()
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(password), nil)
	return writeSecretFile(s.externalSecretPath(id), "v1:"+base64.RawStdEncoding.EncodeToString(sealed))
}

func (s *Server) readExternalSecret(id string) string {
	raw := readSecretFile(s.externalSecretPath(id))
	if !strings.HasPrefix(raw, "v1:") {
		return raw
	}
	key, err := s.externalEncryptionKey()
	if err != nil {
		return ""
	}
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(raw, "v1:"))
	if err != nil {
		return ""
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(data) < gcm.NonceSize() {
		return ""
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return ""
	}
	return string(plain)
}

func (s *Server) externalEncryptionKey() ([]byte, error) {
	path := filepath.Join(s.paths.Secrets, "external.key")
	if raw, err := os.ReadFile(path); err == nil && len(raw) == 32 {
		return raw, nil
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// userSecretPath 是业务账号密码的存放位置：密码只在创建/修改时拿到一次，
// 但用户过两天要配客户端时还得能查回来，所以留一份在本机（0600，仅本机可读）。
func (s *Server) userSecretPath(id, user string) string {
	return filepath.Join(s.paths.Instances, id, "users", user+".secret")
}

func (s *Server) writeUserSecret(id, user, password string) {
	if password == "" {
		return
	}
	_ = writeSecretFile(s.userSecretPath(id, user), password)
}

func (s *Server) readUserSecret(id, user string) string {
	return readSecretFile(s.userSecretPath(id, user))
}

func writeSecretFile(path, secret string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// 先写临时文件再 rename：中途断电也不会留下半个密码
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(secret), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readSecretFile(path string) string {
	buf, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(buf))
}

// newPassword 生成随机密码：大小写字母 + 数字，长度 20。
//
// 不用特殊字符：连接串、命令行、Docker 环境变量里都会被转义，
// 用户复制粘贴到客户端时最容易出问题的就是它们。
func newPassword() (string, error) {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	const length = 20
	buf := make([]byte, length)
	max := big.NewInt(int64(len(charset)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = charset[n.Int64()]
	}
	return string(buf), nil
}

// arch 返回运行架构（amd64 / arm64），用于判断镜像是否有对应平台的构建。
func (s *Server) arch() string {
	return runtime.GOARCH
}

// pickPort 分配实例端口：want>0 时按用户要求校验，否则从端口池里挑第一个可用的。
//
// 三层检查缺一不可：台账（本应用已分配）、Docker（别的容器映射）、系统监听
// （NAS 上别的服务直接占了）。只看台账会出现"创建成功但容器起不来"。
func (s *Server) pickPort(ctx context.Context, want int) (int, error) {
	used, err := s.st.UsedPorts(ctx)
	if err != nil {
		return 0, errInternal("读取已用端口失败")
	}
	available := func(port int) (bool, string) {
		if used[port] {
			return false, "已被本应用的其它服务占用"
		}
		if owner := s.dockerPortOwner(ctx, port); owner != "" {
			return false, "已被容器 " + owner + " 占用"
		}
		if portInUse(port) {
			return false, "已被系统里其它程序占用"
		}
		return true, ""
	}

	if want > 0 {
		if want < 1024 || want > 65535 {
			return 0, errBadRequest("端口要在 1024-65535 之间")
		}
		if ok, why := available(want); !ok {
			return 0, errBadRequest(fmt.Sprintf("端口 %d %s", want, why))
		}
		return want, nil
	}

	for port := portPoolMin; port <= portPoolMax; port++ {
		if ok, _ := available(port); ok {
			return port, nil
		}
	}
	return 0, errBadRequest("端口池 " + strconv.Itoa(portPoolMin) + "-" + strconv.Itoa(portPoolMax) + " 已用尽，请先删除不用的服务")
}

// accessHost 返回给用户连接用的地址：NAS 的局域网 IPv4。
//
// 监听地址是 0.0.0.0，但那不是一个能连的地址；直接把它摆到"连接信息"里，
// 用户复制到客户端一定连不上。这里取第一块非回环网卡的 IPv4，取不到才退回本机。
func (s *Server) accessHost() string {
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() {
				continue
			}
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return "127.0.0.1"
}

// humanSize 把字节数变成人能读的大小，列表与提示里都用它。
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	value := float64(n)
	suffixes := []string{"KB", "MB", "GB", "TB"}
	for _, suffix := range suffixes {
		value /= unit
		if value < unit {
			return strconv.FormatFloat(value, 'f', 1, 64) + " " + suffix
		}
	}
	return strconv.FormatFloat(value, 'f', 1, 64) + " PB"
}

// describeChanges 把配置改动拼成审计 / 任务参数里的一句话。
// 配置值本身不敏感（不是密码），但顺序必须稳定，便于人工比对。
func describeChanges(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+values[k])
	}
	return "配置修改：" + strings.Join(parts, "，")
}
