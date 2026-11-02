// Package dockerctl 用宿主上的 docker 跑数据库实例。
//
// 这一版本只有 Docker 一种运行方式：不再下载引擎二进制、不在本机起 mariadbd、
// 不需要 pid/sock/降权用户。镜像 tag 就是版本，官方镜像覆盖 x86 与 arm64。
//
// 本包只做 docker 命令的薄封装，不关心 SQL；SQL 仍由 mysqlctl 通过 TCP 走，
// 这样权限、配置、导入导出这些逻辑与运行方式解耦。
package dockerctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// firstLine 取报错的第一行：docker 的错误常常多行，界面上只放得下一行。
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return strings.TrimSpace(s)
}

// ErrUnavailable 表示宿主没有可用的 docker。
var ErrUnavailable = errors.New("宿主没有可用的 Docker：请先安装 Docker 并启动服务")

// Client 是 docker 命令行的封装。
type Client struct {
	bin  string
	host string // 允许通过 DOCKER_HOST 指向远端，留空用默认
}

// dockerBinCandidates 是 docker CLI 的常见位置。
//
// 应用以普通应用用户运行，PATH 往往只有 /usr/bin:/bin；NAS 系统还可能把 CLI
// 放在自己的包目录里。只靠 LookPath 会把"装了并且正在跑"的 Docker 误判成没装。
var dockerBinCandidates = []string{
	"/usr/bin/docker",
	"/usr/local/bin/docker",
	"/usr/sbin/docker",
	"/var/packages/Docker/target/usr/bin/docker",
	"/var/packages/Docker/target/bin/docker",
	"/var/packages/ContainerManager/target/usr/bin/docker",
}

// ResolveBinary 找到可用的 docker CLI 绝对路径。
//
// NIUDBM_DOCKER_BIN 可以显式指定（CLI 装在奇怪位置时用），也可以指向一个
// 不存在的路径来显式关掉 Docker 方式——系统里的 docker 服务不想被这个应用碰。
func ResolveBinary() (string, error) {
	if custom := strings.TrimSpace(os.Getenv("NIUDBM_DOCKER_BIN")); custom != "" {
		if fi, err := os.Stat(custom); err == nil && !fi.IsDir() {
			return custom, nil
		}
		return "", fmt.Errorf("NIUDBM_DOCKER_BIN 指向的 %s 不存在", custom)
	}
	if path, err := exec.LookPath("docker"); err == nil {
		return path, nil
	}
	for _, path := range dockerBinCandidates {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			return path, nil
		}
	}
	return "", errors.New("没找到 docker 命令：PATH 与常见安装路径里都没有")
}

// New 返回默认客户端；bin 为空时按 ResolveBinary 找绝对路径。
func New(bin string) *Client {
	if bin == "" {
		if path, err := ResolveBinary(); err == nil {
			bin = path
		} else {
			bin = "docker"
		}
	}
	return &Client{bin: bin}
}

// Available 探测 docker 是否可用，返回服务端版本。
//
// 只看 `docker version` 的 Server 段：装了 docker 但守护进程没起（NAS 上很常见）
// 也必须算不可用，否则用户会在创建实例时才看到莫名其妙的失败。
func (c *Client) Available(ctx context.Context) (string, error) {
	if _, err := exec.LookPath(c.bin); err != nil {
		return "", ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := c.run(ctx, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		// 把 docker 的原话带上去：权限不足和守护进程没起是两种完全不同的修法
		return "", fmt.Errorf("%w（%s）", ErrUnavailable, firstLine(err.Error()))
	}
	version := strings.TrimSpace(out)
	if version == "" {
		return "", ErrUnavailable
	}
	return version, nil
}

func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	if c.host != "" {
		cmd.Env = append(cmd.Environ(), "DOCKER_HOST="+c.host)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("docker %s 失败：%s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// RunOptions 描述一个数据库容器。
type RunOptions struct {
	Name          string // 容器名，用 <引擎>-<版本>-<端口>，与界面上的服务一一对应
	Image         string // 官方镜像，例如 postgres:16-alpine
	InstanceID    string // 打标签，卸载实例时按标签兜底清理
	Engine        string // 打标签：目前固定 mysql
	Port          int    // 宿主端口（绑定 0.0.0.0，NAS 上其他应用要能连）
	ContainerPort int    // 容器内端口（MySQL 3306）
	DataDir       string // 宿主机数据目录，挂到容器里的卷路径
	DataPath      string // 容器内数据路径（/var/lib/mysql）
	// Restart 是 docker 重启策略，留空用 unless-stopped：
	// NAS 重启后服务自己回来，用户手动停止的不会被拉起。
	Restart string
	// UseVolume 为真时 DataDir 是 Docker 命名卷名，而不是宿主目录。
	// 官方镜像会把数据目录 chown 成镜像内用户；Docker Desktop 上绑定挂载的 chown 不生效，
	// 容器会以 "data directory has wrong ownership" 退出，命名卷由 Docker 管属主，跨平台都稳。
	UseVolume bool
	Env       map[string]string // 初始化密码、默认库
	Command   []string          // 追加启动参数（例如 -c max_connections=64）
	// VolumeExtra 是数据目录之外的额外挂载（宿主路径 → 容器路径），
	// 例如 MariaDB 把配置目录挂成 /etc/mysql/conf.d。
	VolumeExtra [][2]string
	// CPUs 是 CPU 核数上限，0 表示不限制（对应 docker run --cpus）。
	CPUs float64
	// MemoryMB 是内存上限（MB），0 表示不限制（对应 docker run --memory）。
	MemoryMB int64
}

// limitArgs 把资源配额翻译成 docker 参数；0 一律视为"不限制"，不传参数。
func limitArgs(cpus float64, memMB int64) []string {
	args := make([]string, 0, 4)
	if cpus > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(cpus, 'f', 2, 64))
	}
	if memMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", memMB))
	}
	return args
}

// Run 创建并启动容器。已存在同名容器时返回错误，避免悄悄复用旧数据。
func (c *Client) Run(ctx context.Context, opt RunOptions) error {
	args := []string{
		"run", "-d",
		"--name", opt.Name,
		"--label", "niudbm.managed=true",
		"--label", "niudbm.instance=" + opt.InstanceID,
		"--label", "niudbm.engine=" + opt.Engine,
		"--restart", restartPolicy(opt.Restart),
		"-p", fmt.Sprintf("0.0.0.0:%d:%d", opt.Port, opt.ContainerPort),
		"-v", opt.DataDir + ":" + opt.DataPath,
	}
	for _, vol := range opt.VolumeExtra {
		args = append(args, "-v", vol[0]+":"+vol[1])
	}
	args = append(args, limitArgs(opt.CPUs, opt.MemoryMB)...)
	// 环境变量排序输出，保证日志与测试可复现
	for _, k := range sortedKeys(opt.Env) {
		args = append(args, "-e", k+"="+opt.Env[k])
	}
	args = append(args, opt.Image)
	args = append(args, opt.Command...)
	_, err := c.run(ctx, args...)
	return err
}

// CheckCnf 让 mysqld 在本机解析一遍配置目录，提前判断参数是否被这个镜像接受。
//
// 为什么要多起一个临时容器：官方 entrypoint 靠 `mysqld --verbose --help` 的输出反推
// datadir/socket，解析一旦失败两个值都是空串，`dirname ""` 得到 "."，紧接着就是对
// 容器根目录做 `find . -exec chown`——日志被 /sys 的只读报错刷屏，真正的参数错误
// 反而完全看不见。这里用 --entrypoint 绕开 entrypoint 自己跑一次，把 mysqld 的原话留下。
func (c *Client) CheckCnf(ctx context.Context, image, hostDir, containerDir string, extra []string) error {
	if strings.TrimSpace(hostDir) == "" {
		return nil
	}
	// 参数顺序与 entrypoint 完全一致：`mysqld <容器的 CMD...> --verbose --help`。
	// 少了 CMD 这一段，校验就测不到"启动参数本身不被接受"这类失败。
	args := []string{
		"run", "--rm",
		"--entrypoint", "mysqld",
		"-v", hostDir + ":" + containerDir + ":ro",
		image,
	}
	args = append(args, extra...)
	args = append(args, "--verbose", "--help")
	if _, err := c.run(ctx, args...); err != nil {
		return fmt.Errorf("镜像 %s 不接受当前启动参数或配置：%s", image, cnfCause(err.Error()))
	}
	return nil
}

// cnfCause 从一条可能非常长的命令输出里挑出真正的原因。
//
// mysqld 的报错未必在第一行（前面可能还有大量与本次失败无关的输出），
// 所以优先找带 error/unknown/invalid 的行，实在没有就退回最后一行。
func cnfCause(msg string) string {
	var last string
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		low := strings.ToLower(line)
		if strings.Contains(low, "error") || strings.Contains(low, "unknown") ||
			strings.Contains(low, "invalid") || strings.Contains(low, "failed") {
			return line
		}
		last = line
	}
	if last != "" {
		return last
	}
	return firstLine(msg)
}

// State 返回容器状态与启动参数，用于"容器没有日志"时定位问题。
//
// 容器停在 created（从未跑起来）与 exited（跑完就挂）是两种完全不同的原因，
// 只看一句"没有输出日志"没法判断。
func (c *Client) State(ctx context.Context, name string) string {
	out, err := c.run(ctx, "inspect", "-f",
		"status={{.State.Status}} exit={{.State.ExitCode}} error={{.State.Error}} cmd={{json .Config.Cmd}}",
		name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// restartPolicy 统一重启策略的默认值。
func restartPolicy(want string) string {
	if strings.TrimSpace(want) == "" {
		return "unless-stopped"
	}
	return want
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// Start 启动已存在的容器。
func (c *Client) Start(ctx context.Context, name string) error {
	_, err := c.run(ctx, "start", name)
	return err
}

// Stop 停止容器（10 秒内没停就杀掉，避免关不掉的实例卡住界面）。
func (c *Client) Stop(ctx context.Context, name string) error {
	_, err := c.run(ctx, "stop", "-t", "10", name)
	return err
}

// Remove 强制删除容器；容器不存在时视为成功。
func (c *Client) Remove(ctx context.Context, name string) error {
	if !c.Exists(ctx, name) {
		return nil
	}
	_, err := c.run(ctx, "rm", "-f", name)
	return err
}

// Restart 重启容器（配置项需要重启时用）。
func (c *Client) Restart(ctx context.Context, name string) error {
	_, err := c.run(ctx, "restart", "-t", "10", name)
	return err
}

// UpdateLimits 热改运行中容器的资源配额，不必重建容器。
//
// 只下发大于 0 的项：docker update 不接受 --cpus 0（要求 >= 0.01），
// 所以"改成不限制"没法热生效，得重建容器；这种情形下面的入参会跳过，
// 台账里仍记新值，界面上按台账显示。
func (c *Client) UpdateLimits(ctx context.Context, name string, cpus float64, memMB int64) error {
	args := []string{"update"}
	args = append(args, limitArgs(cpus, memMB)...)
	if len(args) == 1 {
		return nil
	}
	if !c.Exists(ctx, name) {
		return fmt.Errorf("容器 %s 不存在", name)
	}
	args = append(args, name)
	_, err := c.run(ctx, args...)
	return err
}

// Exists 判断容器是否存在（运行或已停止都算）。
func (c *Client) Exists(ctx context.Context, name string) bool {
	_, err := c.run(ctx, "inspect", "--format", "{{.Id}}", name)
	return err == nil
}

// Running 判断容器是否在运行。
func (c *Client) Running(ctx context.Context, name string) bool {
	out, err := c.run(ctx, "inspect", "--format", "{{.State.Running}}", name)
	return err == nil && strings.TrimSpace(out) == "true"
}

// Status 返回容器状态文字（running / exited / created…），用于把 docker 的真实状态回显到台账。
func (c *Client) Status(ctx context.Context, name string) string {
	out, err := c.run(ctx, "inspect", "--format", "{{.State.Status}}", name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// PortOwner 返回占用某个宿主端口的容器名（没有则空）。
//
// docker 的端口发布是自己管的：宿主上 `ss` 看到的进程是 docker-proxy，
// 分辨不出是哪个容器，所以用 `docker ps` 的端口映射反查。
func (c *Client) PortOwner(ctx context.Context, port int) string {
	out, err := c.run(ctx, "ps", "--format", "{{.Names}}\t{{.Ports}}")
	if err != nil {
		return ""
	}
	needle := ":" + strconv.Itoa(port) + "->"
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.Contains(parts[1], needle) || strings.Contains(parts[1], ":"+strconv.Itoa(port)+"/") {
			return strings.TrimSpace(parts[0])
		}
	}
	return ""
}

// Logs 取容器日志尾部，用于实例日志页与失败排查。
func (c *Client) Logs(ctx context.Context, name string, lines int) (string, error) {
	return c.run(ctx, "logs", "--tail", strconv.Itoa(lines), name)
}

// Pull 拉镜像。已存在时 docker 会很快返回。
func (c *Client) Pull(ctx context.Context, image string) error {
	_, err := c.run(ctx, "pull", image)
	return err
}

// Tag 给本地镜像再打一个名字。
//
// 从加速站拉下来的标签带镜像站前缀；统一改成官方名之后，
// docker ps 里显示的名字才与界面一致，后续实例也能直接复用。
func (c *Client) Tag(ctx context.Context, src, dst string) error {
	_, err := c.run(ctx, "tag", src, dst)
	return err
}

// ImageExists 判断镜像在本地是否已存在。
func (c *Client) ImageExists(ctx context.Context, image string) bool {
	_, err := c.run(ctx, "image", "inspect", "--format", "{{.Id}}", image)
	return err == nil
}

// ImageRemove 删除本地镜像。
//
// 容器还在用这个镜像时 docker 会拒绝，把原话带回去——调用方要在动手之前
// 先按台账检查占用，不能指望 docker 帮你兜底。
func (c *Client) ImageRemove(ctx context.Context, image string) error {
	_, err := c.run(ctx, "rmi", image)
	return err
}

// ExecStream 在容器里执行命令，把标准输出直接写进 out（备份大库时不必整份读进内存）。
//
// env 必须在容器名之前展开成 -e：docker exec 的选项只能出现在 CONTAINER 之前，
// 放到后面会被当成命令的一部分（表现为 exit status 127）。
func (c *Client) ExecStream(ctx context.Context, name string, out io.Writer, stdin io.Reader, env map[string]string, args ...string) error {
	full := []string{"exec", "-i"}
	for _, k := range sortedKeys(env) {
		full = append(full, "-e", k+"="+env[k])
	}
	full = append(full, name)
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, c.bin, full...)
	if c.host != "" {
		cmd.Env = append(cmd.Environ(), "DOCKER_HOST="+c.host)
	}
	cmd.Stdin = stdin
	cmd.Stdout = out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("容器内执行 %s 失败：%s", strings.Join(args, " "), msg)
	}
	return nil
}

// Exec 在容器里执行命令，stdin 可为空；返回标准输出。
func (c *Client) Exec(ctx context.Context, name string, env map[string]string, stdin io.Reader, args ...string) (string, error) {
	full := []string{"exec", "-i"}
	for _, k := range sortedKeys(env) {
		full = append(full, "-e", k+"="+env[k])
	}
	full = append(full, name)
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, c.bin, full...)
	if c.host != "" {
		cmd.Env = append(cmd.Environ(), "DOCKER_HOST="+c.host)
	}
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("容器内执行 %s 失败：%s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// RemoveVolume 删除数据卷；卷不存在时视为成功。
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	_, err := c.run(ctx, "volume", "rm", "-f", name)
	return err
}

// VolumeExists 判断命名卷是否存在。
func (c *Client) VolumeExists(ctx context.Context, name string) bool {
	_, err := c.run(ctx, "volume", "inspect", "--format", "{{.Name}}", name)
	return err == nil
}
