// Package platform 负责读取 fnOS 注入的 TRIM_* 环境变量。
//
// 平台只在应用进程由系统拉起时注入这些变量；开发模式下（直接 go run）它们不存在，
// 因此这里统一提供回退值，并用 InPlatform 标记当前是否真的运行在平台环境内。
package platform

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Env 是应用运行时依赖的全部平台信息。
type Env struct {
	AppName     string
	Version     string
	InPlatform  bool
	AppDest     string
	PkgVar      string
	PkgEtc      string
	PkgTmp      string
	PkgHome     string
	RunUser     string
	UID         int
	GID         int
	SysArch     string
	SysVersion  string
	TempLog     string
	HasAPIToken bool

	// DataSharePaths 来自 TRIM_DATA_SHARE_PATHS，即 config/resource 声明的共享目录
	DataSharePaths []string
	// AccessiblePaths 来自 TRIM_DATA_ACCESSIBLE_PATHS，即用户授权的目录
	AccessiblePaths []string
}

const defaultAppName = "niudbm"

// Load 读取环境变量；version 由构建时注入。
func Load(version string) Env {
	appName := firstNonEmpty(os.Getenv("TRIM_APPNAME"), defaultAppName)
	inPlatform := os.Getenv("TRIM_APPDEST") != ""

	// 平台内用 /var/apps/<app> 下的真实软链；开发模式没有这些路径也没有权限，
	// 改用临时目录模拟一套，让自检结果反映真实可写性而不是一片 permission denied。
	base := filepath.Join("/var/apps", appName)
	if !inPlatform {
		base = filepath.Join(os.TempDir(), appName+"-dev")
	}

	env := Env{
		AppName:         appName,
		Version:         version,
		InPlatform:      inPlatform,
		AppDest:         firstNonEmpty(os.Getenv("TRIM_APPDEST"), filepath.Join(base, "target")),
		PkgVar:          firstNonEmpty(os.Getenv("TRIM_PKGVAR"), filepath.Join(base, "var")),
		PkgEtc:          firstNonEmpty(os.Getenv("TRIM_PKGETC"), filepath.Join(base, "etc")),
		PkgTmp:          firstNonEmpty(os.Getenv("TRIM_PKGTMP"), filepath.Join(base, "tmp")),
		PkgHome:         firstNonEmpty(os.Getenv("TRIM_PKGHOME"), filepath.Join(base, "home")),
		RunUser:         os.Getenv("TRIM_USERNAME"),
		UID:             firstInt(os.Getenv("TRIM_UID"), os.Getuid()),
		GID:             firstInt(os.Getenv("TRIM_GID"), os.Getgid()),
		SysArch:         os.Getenv("TRIM_SYS_ARCH"),
		SysVersion:      os.Getenv("TRIM_SYS_VERSION"),
		TempLog:         os.Getenv("TRIM_TEMP_LOGFILE"),
		HasAPIToken:     os.Getenv("TRIM_API_TOKEN") != "",
		DataSharePaths:  splitPaths(os.Getenv("TRIM_DATA_SHARE_PATHS")),
		AccessiblePaths: splitPaths(os.Getenv("TRIM_DATA_ACCESSIBLE_PATHS")),
	}
	return env
}

// TrimVarCount 返回当前进程收到的 TRIM_* 变量个数，用于判断是否真的在平台内运行。
func TrimVarCount() int {
	count := 0
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TRIM_") {
			count++
		}
	}
	return count
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstInt(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

// splitPaths 按平台约定使用冒号分隔多路径。
func splitPaths(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ":")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
