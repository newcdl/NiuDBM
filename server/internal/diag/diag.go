// Package diag 把平台能力验证做成服务端实时检查。
//
// 设计原则：每条结论都必须来自一次真实的系统调用（写文件、绑端口、读属性），
// 不做"看起来正常"的推断，也不打印任何凭据内容。
package diag

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"niudbm/server/internal/platform"
)

// Level 是检查结论，只有三态。
type Level string

const (
	Pass Level = "pass"
	Warn Level = "warn"
	Fail Level = "fail"
)

// Item 是一条检查结果。
type Item struct {
	ID     string `json:"id"`
	Level  Level  `json:"level"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
	// Ref 关联核对清单编号，便于把界面结果直接对回文档
	Ref string `json:"ref,omitempty"`
}

// Group 是一组检查结果。
type Group struct {
	Name  string `json:"name"`
	Items []Item `json:"items"`
}

// EnvInfo 是给前端展示的运行环境摘要。
type EnvInfo struct {
	AppName string `json:"appName"`
	Version string `json:"version"`
	User    string `json:"user"`
	UID     int    `json:"uid"`
	GID     int    `json:"gid"`
	IsAdmin bool   `json:"isAdmin"`
	Arch    string `json:"arch"`
	Listen  string `json:"listen"`
	OSName  string `json:"osName"`
	Glibc   string `json:"glibc"`
}

// Summary 是三态统计。
type Summary struct {
	Pass  int `json:"pass"`
	Warn  int `json:"warn"`
	Fail  int `json:"fail"`
	Total int `json:"total"`
}

// Report 是一次完整自检的结果。
type Report struct {
	GeneratedAt time.Time `json:"generatedAt"`
	Env         EnvInfo   `json:"env"`
	Summary     Summary   `json:"summary"`
	Groups      []Group   `json:"groups"`
}

// Run 执行全部检查。Listen 与 IsAdmin 由调用方（HTTP 层）补填。
func Run(env platform.Env) Report {
	groups := []Group{
		identityGroup(env),
		pathGroup(env),
		boundaryGroup(env),
		systemGroup(env),
		networkGroup(),
		platformGroup(env),
		lifecycleGroup(env),
	}

	report := Report{
		GeneratedAt: time.Now(),
		Env: EnvInfo{
			AppName: env.AppName,
			Version: env.Version,
			User:    displayUser(env),
			UID:     env.UID,
			GID:     env.GID,
			Arch:    runtime.GOARCH,
			OSName:  osName(),
			Glibc:   glibcVersion(),
		},
		Groups: groups,
	}
	report.Summary = summarize(groups)
	return report
}

func summarize(groups []Group) Summary {
	var s Summary
	for _, g := range groups {
		for _, item := range g.Items {
			switch item.Level {
			case Pass:
				s.Pass++
			case Warn:
				s.Warn++
			default:
				s.Fail++
			}
			s.Total++
		}
	}
	return s
}

// ---------- 分组检查 ----------

func identityGroup(env platform.Env) Group {
	items := []Item{}
	uid, gid := os.Getuid(), os.Getgid()

	if uid == 0 {
		items = append(items, Item{
			ID: "identity", Level: Fail, Name: "运行身份", Ref: "P-05",
			Detail: "以 root 运行（uid=0），最小权限前提不成立",
		})
	} else {
		items = append(items, Item{
			ID: "identity", Level: Pass, Name: "运行身份", Ref: "P-05",
			Detail: fmt.Sprintf("uid=%d gid=%d%s", uid, gid, userSuffix(env)),
		})
	}

	if env.InPlatform {
		if uid == env.UID {
			items = append(items, Item{
				ID: "identity-match", Level: Pass, Name: "身份与声明一致", Ref: "P-05",
				Detail: fmt.Sprintf("TRIM_UID=%d 与实际一致", env.UID),
			})
		} else {
			items = append(items, Item{
				ID: "identity-match", Level: Warn, Name: "身份与声明不一致", Ref: "P-05",
				Detail: fmt.Sprintf("TRIM_UID=%d，实际 uid=%d", env.UID, uid),
			})
		}
	} else {
		items = append(items, Item{
			ID: "identity-match", Level: Warn, Name: "未运行在平台环境", Ref: "P-05",
			Detail: "未收到 TRIM_* 变量，当前应为开发模式直连",
		})
	}

	// 子进程身份：引擎进程必须与主进程同权，提前把当前进程身份记下来
	if name := os.Getenv("TRIM_RUN_USERNAME"); name == "root" && uid != 0 {
		items = append(items, Item{
			ID: "identity-drop", Level: Warn, Name: "脚本执行身份", Ref: "P-07",
			Detail: "脚本曾以 root 执行，拉起引擎时必须显式降权",
		})
	}

	return Group{Name: "运行身份", Items: items}
}

func pathGroup(env platform.Env) Group {
	items := []Item{}

	targets := []struct {
		id, name, path string
	}{
		{"path-var", "数据目录 var", env.PkgVar},
		{"path-etc", "配置目录 etc", env.PkgEtc},
		{"path-tmp", "临时目录 tmp", env.PkgTmp},
		{"path-dest", "安装目录 target", env.AppDest},
		{"path-home", "用户目录 home", env.PkgHome},
	}

	for _, t := range targets {
		if ok, detail := probeWritable(t.path); ok {
			items = append(items, Item{ID: t.id, Level: Pass, Name: t.name, Detail: detail, Ref: "P-02"})
		} else {
			items = append(items, Item{ID: t.id, Level: Fail, Name: t.name, Detail: detail, Ref: "P-02"})
		}
	}

	if len(env.DataSharePaths) == 0 {
		items = append(items, Item{
			ID: "path-share", Level: Warn, Name: "共享目录", Ref: "P-12",
			Detail: "未收到 TRIM_DATA_SHARE_PATHS，无法验证 data-share",
		})
	} else {
		for i, share := range env.DataSharePaths {
			if ok, detail := probeWritable(share); ok {
				items = append(items, Item{
					ID: "share-" + strconv.Itoa(i), Level: Pass, Name: "共享目录 " + filepath.Base(share),
					Detail: detail, Ref: "P-12",
				})
			} else {
				items = append(items, Item{
					ID: "share-" + strconv.Itoa(i), Level: Fail, Name: "共享目录 " + filepath.Base(share),
					Detail: detail, Ref: "P-12",
				})
			}
		}
	}

	return Group{Name: "目录与路径", Items: items}
}

func boundaryGroup(env platform.Env) Group {
	items := []Item{}

	checks := []struct {
		id, name, path, ref string
	}{
		{"deny-etc", "写入 /etc", "/etc", "P-03"},
		{"deny-apps", "写入其他应用目录", "/var/apps", "P-03"},
		{"deny-docker", "访问 Docker Socket", "/var/run/docker.sock", "P-03"},
	}

	for _, c := range checks {
		denied, detail := probeDenied(c.path)
		level := Pass
		if !denied {
			level = Fail
		}
		items = append(items, Item{ID: c.id, Level: level, Name: c.name, Detail: detail, Ref: c.ref})
	}

	if len(env.AccessiblePaths) > 0 {
		items = append(items, Item{
			ID: "allow-paths", Level: Pass, Name: "用户授权目录",
			Detail: fmt.Sprintf("收到 %d 个授权路径", len(env.AccessiblePaths)), Ref: "P-16",
		})
	} else {
		items = append(items, Item{
			ID: "allow-paths", Level: Warn, Name: "用户授权目录", Ref: "P-16",
			Detail: "尚无授权目录，导入导出将退化为上传下载",
		})
	}

	return Group{Name: "权限边界", Items: items}
}

func systemGroup(env platform.Env) Group {
	items := []Item{}

	arch := runtime.GOARCH
	if env.SysArch != "" && normalizeArch(env.SysArch) != normalizeArch(arch) {
		items = append(items, Item{
			ID: "sys-arch", Level: Warn, Name: "架构一致性", Ref: "P-17",
			Detail: fmt.Sprintf("TRIM_SYS_ARCH=%s，实际 %s", env.SysArch, arch),
		})
	} else {
		items = append(items, Item{
			ID: "sys-arch", Level: Pass, Name: "CPU 架构", Ref: "P-17",
			Detail: fmt.Sprintf("%s（系统声明 %s）", arch, orUnknown(env.SysArch)),
		})
	}

	items = append(items, Item{
		ID: "sys-os", Level: Pass, Name: "操作系统",
		Detail: fmt.Sprintf("%s（系统版本 %s）", osName(), orUnknown(env.SysVersion)),
	})

	if glibc := glibcVersion(); glibc != "未知" {
		items = append(items, Item{
			ID: "sys-glibc", Level: Pass, Name: "C 运行库", Ref: "P-18", Detail: glibc,
		})
	} else {
		items = append(items, Item{
			ID: "sys-glibc", Level: Warn, Name: "C 运行库", Ref: "P-18",
			Detail: "无法读取 glibc 版本，引擎兼容性基线无法建立",
		})
	}

	items = append(items, Item{
		ID: "sys-hw", Level: Pass, Name: "硬件资源", Detail: hardwareSummary(),
	})

	return Group{Name: "系统环境", Items: items}
}

func networkGroup() Group {
	items := []Item{}
	busy := []string{}
	for port := 33000; port <= 33004; port++ {
		if !portFree(port) {
			busy = append(busy, strconv.Itoa(port))
		}
	}

	if len(busy) == 0 {
		items = append(items, Item{
			ID: "net-port", Level: Pass, Name: "端口池", Ref: "P-19",
			Detail: "33000-33004 在 127.0.0.1 上均可绑定",
		})
	} else {
		items = append(items, Item{
			ID: "net-port", Level: Warn, Name: "端口池", Ref: "P-19",
			Detail: "以下端口已被占用：" + strings.Join(busy, ", "),
		})
	}

	items = append(items, Item{
		ID: "net-expose", Level: Warn, Name: "对外暴露", Ref: "P-19",
		Detail: "端口探测只验证本机可绑定，是否对外可达需在真机用局域网设备实测",
	})

	return Group{Name: "端口与网络", Items: items}
}

func platformGroup(env platform.Env) Group {
	items := []Item{}
	count := platform.TrimVarCount()

	if count > 0 {
		items = append(items, Item{
			ID: "trim-vars", Level: Pass, Name: "平台变量注入",
			Detail: fmt.Sprintf("共收到 %d 个 TRIM_* 变量", count), Ref: "P-02",
		})
	} else {
		items = append(items, Item{
			ID: "trim-vars", Level: Warn, Name: "平台变量注入", Ref: "P-02",
			Detail: "未收到 TRIM_* 变量，当前不在平台运行环境内",
		})
	}

	if env.HasAPIToken {
		items = append(items, Item{
			ID: "trim-token", Level: Pass, Name: "开放 API 令牌", Ref: "P-15",
			Detail: "TRIM_API_TOKEN 已注入（内容不展示）",
		})
	} else {
		items = append(items, Item{
			ID: "trim-token", Level: Warn, Name: "开放 API 令牌", Ref: "P-15",
			Detail: "未注入 TRIM_API_TOKEN，文件选择与授权查询不可用",
		})
	}

	return Group{Name: "平台集成", Items: items}
}

func lifecycleGroup(env platform.Env) Group {
	items := []Item{}

	installedAt := readTrimmed(env.PkgVar + "/installed_at")
	if installedAt == "" {
		items = append(items, Item{
			ID: "lf-installed", Level: Warn, Name: "安装标记", Ref: "P-20",
			Detail: "未找到 installed_at，install_callback 可能未执行",
		})
	} else {
		items = append(items, Item{
			ID: "lf-installed", Level: Pass, Name: "安装标记", Ref: "P-20", Detail: installedAt,
		})
	}

	if hb := readTrimmed(env.PkgVar + "/heartbeat"); hb == "" {
		items = append(items, Item{
			ID: "lf-heartbeat", Level: Warn, Name: "服务心跳", Ref: "P-21",
			Detail: "未找到 heartbeat，服务未启动时属正常",
		})
	} else {
		items = append(items, Item{
			ID: "lf-heartbeat", Level: Pass, Name: "服务心跳", Ref: "P-21",
			Detail: "最近心跳 " + hb,
		})
	}

	return Group{Name: "生命周期", Items: items}
}

// ---------- 探测原语 ----------

func probeWritable(path string) (bool, string) {
	if path == "" {
		return false, "路径为空，无法检查"
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return false, "无法创建：" + err.Error()
	}
	f, err := os.CreateTemp(path, ".niudbm-probe-*")
	if err != nil {
		return false, "目录存在但不可写 · " + path
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)

	real := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		real = resolved
	}
	return true, "可读写 · " + real
}

// probeDenied 检查越权访问是否被拒绝。denied=true 表示边界有效。
func probeDenied(path string) (bool, string) {
	fi, err := os.Stat(path)
	if err != nil {
		return true, "路径不存在，天然无法访问"
	}

	if fi.IsDir() {
		f, err := os.CreateTemp(path, ".niudbm-probe-*")
		if err != nil {
			return true, "写入被拒绝，边界有效"
		}
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
		return false, "意外写入成功，存在越权风险 · " + path
	}

	// socket 等特殊文件只用权限位判断，绝不打开，避免影响服务端连接
	if writableBy(fi) {
		return false, "对当前用户可写，存在越权风险 · " + path
	}
	return true, "不可写，边界有效"
}

func writableBy(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	perm := fi.Mode().Perm()
	switch {
	case int(st.Uid) == os.Geteuid():
		return perm&0o200 != 0
	case inGroup(int(st.Gid)):
		return perm&0o020 != 0
	default:
		return perm&0o002 != 0
	}
}

func inGroup(gid int) bool {
	if gid == os.Getegid() {
		return true
	}
	groups, err := os.Getgroups()
	if err != nil {
		return false
	}
	for _, g := range groups {
		if g == gid {
			return true
		}
	}
	return false
}

func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// ---------- 系统信息 ----------

func osName() string {
	raw, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return runtime.GOOS
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
		}
	}
	return runtime.GOOS
}

func glibcVersion() string {
	out, err := exec.Command("ldd", "--version").Output()
	if err != nil || len(out) == 0 {
		return "未知"
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if line == "" {
		return "未知"
	}
	return line
}

func hardwareSummary() string {
	parts := []string{fmt.Sprintf("CPU %d 核", runtime.NumCPU())}
	if raw, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if kb, err := strconv.Atoi(fields[1]); err == nil {
						parts = append(parts, fmt.Sprintf("内存 %d MB", kb/1024))
					}
				}
				break
			}
		}
	}
	return strings.Join(parts, " · ")
}

func readTrimmed(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func userSuffix(env platform.Env) string {
	if env.RunUser != "" {
		return "（" + env.RunUser + "）"
	}
	if u := os.Getenv("USER"); u != "" {
		return "（" + u + "）"
	}
	return ""
}

// displayUser 优先用平台声明的运行用户，开发模式下退回当前 shell 用户。
func displayUser(env platform.Env) string {
	if env.RunUser != "" {
		return env.RunUser
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "未知"
}

func normalizeArch(arch string) string {
	switch arch {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return arch
}

func orUnknown(v string) string {
	if v == "" {
		return "未注入"
	}
	return v
}

// PrintText 输出给人看的纯文本报告，供 SSH 场景使用。
func PrintText(r Report) {
	fmt.Println("==============================================")
	fmt.Printf(" 牛库管家 · 平台自检（v%s）\n", r.Env.Version)
	fmt.Printf(" 时间：%s\n", r.GeneratedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf(" 身份：uid=%d gid=%d · 架构 %s\n", r.Env.UID, r.Env.GID, r.Env.Arch)
	fmt.Println("==============================================")

	for _, g := range r.Groups {
		fmt.Printf("\n[%s]\n", g.Name)
		for _, item := range g.Items {
			ref := ""
			if item.Ref != "" {
				ref = " (" + item.Ref + ")"
			}
			fmt.Printf("  %-4s %s%s\n        %s\n", strings.ToUpper(string(item.Level)), item.Name, ref, item.Detail)
		}
	}

	fmt.Println("\n----------------------------------------------")
	fmt.Printf(" 通过 %d · 需确认 %d · 未通过 %d · 共 %d 项\n",
		r.Summary.Pass, r.Summary.Warn, r.Summary.Fail, r.Summary.Total)

	switch {
	case r.Summary.Fail > 0:
		fmt.Println(" 结论：存在未通过项，请逐条对照核对清单处理后再继续。")
	case r.Summary.Warn > 0:
		fmt.Println(" 结论：无阻断项，需确认项请人工判断。")
	default:
		fmt.Println(" 结论：全部通过。")
	}
}
