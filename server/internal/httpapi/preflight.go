package httpapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"niudbm/server/internal/dockerctl"
)

// 部署前体检：这台机器上有没有别的数据库、常用端口占没占、Docker 能不能用。
//
// 之所以要在部署之前查这些：NAS 上很可能已经跑着系统自带的 MySQL，
// 或者别的容器占了 3306。本应用的实例固定用 33000+ 端口池，
// 不会去抢这些端口，但用户需要知道"这两个东西是并存的"，而不是装完发现连错库。

// watchedPorts 是用户最容易混淆的端口：本应用不用它们，但冲突时要解释清楚。
var watchedPorts = []int{5432, 3306, 3307, 6379, 27017}

type portStatus struct {
	Port   int    `json:"port"`
	InUse  bool   `json:"inUse"`
	Engine string `json:"engine,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

type dockerStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version,omitempty"`
	Note      string `json:"note,omitempty"`
}

type hostInstall struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"` // binary | datadir | config
}

type preflightResult struct {
	Arch      string        `json:"arch"`
	FreeBytes int64         `json:"freeBytes"`
	Ports     []portStatus  `json:"ports"`
	Docker    dockerStatus  `json:"docker"`
	Existing  []hostInstall `json:"existing"`
	Warnings  []string      `json:"warnings"`
	Checks    []string      `json:"checks"`
}

// handlePreflight 返回安装前的环境体检结果。
func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, false); !ok {
		return
	}
	writeOK(w, r, s.preflight())
}

func (s *Server) preflight() preflightResult {
	arch := s.arch()
	out := preflightResult{
		Arch:      arch,
		FreeBytes: freeBytes(s.env.PkgVar),
		Docker:    probeDocker(),
		Existing:  detectHostInstalls(),
	}
	out.Checks = append(out.Checks, "运行架构 "+arch)
	if out.FreeBytes > 0 {
		out.Checks = append(out.Checks, "数据目录所在磁盘可用 "+humanSize(out.FreeBytes))
	}
	if out.FreeBytes > 0 && out.FreeBytes < 2<<30 {
		out.Warnings = append(out.Warnings, "磁盘可用不足 2GB：镜像与数据都放在这块盘上，建议先清理空间")
	}

	for _, port := range watchedPorts {
		status := portStatus{Port: port, InUse: portInUse(port), Engine: portEngineName(port)}
		if status.InUse {
			status.Hint = fmt.Sprintf("%d 已被占用；本应用的实例用 33000+ 端口池，不会冲突，连接时别连错", port)
		}
		out.Ports = append(out.Ports, status)
	}

	for _, item := range out.Existing {
		switch item.Kind {
		case "binary":
			out.Warnings = append(out.Warnings, "检测到系统里已有 "+item.Name+"（"+item.Path+"）：本应用只读写自己的数据目录，不会碰它")
		case "datadir":
			out.Warnings = append(out.Warnings, "检测到已有数据目录 "+item.Path+"：请勿删除，本应用不会复用也不会修改")
		}
	}
	if !out.Docker.Running {
		if out.Docker.Note != "" {
			out.Warnings = append(out.Warnings, "Docker 不可用："+out.Docker.Note)
		} else if out.Docker.Installed {
			out.Warnings = append(out.Warnings, "装了 Docker 但守护进程没在跑：Docker 方式的服务无法启动")
		}
	}
	return out
}

// portInUse 用"能不能绑上"来判断占用，不依赖 ss/lsof：宿主缺这些命令时也能用。
func portInUse(port int) bool {
	for _, addr := range []string{"0.0.0.0:" + strconv.Itoa(port), "127.0.0.1:" + strconv.Itoa(port)} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return true
		}
		_ = ln.Close()
	}
	return false
}

func portEngineName(port int) string {
	switch port {
	case 3306, 3307:
		return "MySQL/MariaDB 默认端口"
	case 6379:
		return "Redis 默认端口"
	case 27017:
		return "MongoDB 默认端口"
	}
	return ""
}

// probeDocker 只做"有没有、跑没跑"的粗判，4 秒超时避免卡住页面。
//
// 三种失败必须分开报，因为修法完全不同：
//   - 找不到 CLI：应用用户的 PATH 不含 docker，或者装在了别处
//   - permission denied：应用用户不在 docker 组，读不了 /var/run/docker.sock
//   - 连不上守护进程：系统里的 Docker 服务确实没起
//
// 以前一律置 false 且不给原因，界面上就只剩"没有可用版本"，没法排查。
func probeDocker() dockerStatus {
	var status dockerStatus
	bin, err := dockerctl.ResolveBinary()
	if err != nil {
		status.Note = err.Error() + "（Docker 方式需要宿主提供 docker 命令）"
		return status
	}
	status.Installed = true

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "version", "--format", "{{.Server.Version}}")
	out, runErr := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))

	if ctx.Err() == context.DeadlineExceeded {
		status.Note = "docker 命令 4 秒没有响应（守护进程可能假死），暂按不可用处理"
		return status
	}
	if runErr != nil {
		status.Note = dockerErrHint(text)
		return status
	}
	if v := firstLineOf(text); v != "" {
		status.Running = true
		status.Version = v
	}
	return status
}

// dockerErrHint 把 docker 的原始报错翻成能照着做的中文提示。
func dockerErrHint(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "permission denied"):
		// 直接把 socket 属组和修复命令带出来，用户不用再猜是哪个组
		return dockerctl.PermissionHint()
	case strings.Contains(lower, "cannot connect"), strings.Contains(lower, "is the docker daemon running"),
		strings.Contains(lower, "connection refused"), strings.Contains(lower, "no such file"):
		return "连不上 Docker 守护进程：确认系统里的 Docker 服务正在运行"
	case raw != "":
		return firstLineOf(raw)
	}
	return "docker version 执行失败，原因未知"
}

func firstLineOf(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return strings.TrimSpace(s)
}

// detectHostInstalls 找系统里已有的数据库安装痕迹，只读不改。
func detectHostInstalls() []hostInstall {
	var out []hostInstall
	seen := map[string]bool{}
	add := func(item hostInstall) {
		key := item.Name + "|" + item.Path
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, item)
	}

	for _, bin := range []string{"mariadbd", "mysqld", "mysql"} {
		if path, err := exec.LookPath(bin); err == nil {
			add(hostInstall{Name: bin, Path: path, Kind: "binary"})
		}
	}
	for _, dir := range []string{"/var/lib/mysql", "/etc/mysql"} {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			kind := "datadir"
			if strings.HasPrefix(dir, "/etc") {
				kind = "config"
			}
			add(hostInstall{Name: filepath.Base(dir), Path: dir, Kind: kind})
		}
	}
	return out
}

// freeBytes 返回 path 所在分区的可用字节数。
//
// path 可能还没建出来（首次启动前 var 目录不存在），此时往上找第一个存在的祖先目录：
// 直接对不存在的路径 Statfs 只会拿到错误，界面上就变成"磁盘可用 0"，看着像环境坏了。
func freeBytes(path string) int64 {
	probe := path
	for probe != "" && probe != "/" {
		if _, err := os.Stat(probe); err == nil {
			break
		}
		probe = filepath.Dir(probe)
	}
	if probe == "" {
		probe = "/"
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(probe, &stat); err != nil {
		return 0
	}
	return int64(stat.Bavail) * int64(stat.Bsize)
}
