package dockerctl

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// DefaultSocket 是 dockerd 的默认监听路径。
//
// 不提供多路径探测：真机上要换 socket 通常是走了 remote/rootless 模式，
// 那种情况得显式告诉用户去设 DOCKER_HOST，靠瞎猜路径只会让排查更乱。
const DefaultSocket = "/var/run/docker.sock"

// SocketGID 返回 socket 的属组；socket 不存在时 ok=false。
func SocketGID() (int64, bool) {
	fi, err := os.Stat(DefaultSocket)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int64(st.Gid), true
}

// GroupName 把 gid 翻成组名；查不到就退回数字，别让提示变成空的。
func GroupName(gid int64) string {
	if gid < 0 {
		return "未知"
	}
	id := strconv.FormatInt(gid, 10)
	if g, err := user.LookupGroupId(id); err == nil && g.Name != "" {
		return g.Name
	}
	return id
}

// CurrentUserName 返回当前进程的用户名，用于提示里对照。
func CurrentUserName() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "uid=" + strconv.Itoa(os.Getuid())
}

// PermissionHint 给出"读不了 socket"时能照着做的提示。
//
// NAS 上这条几乎总是同一个原因：应用以普通应用用户运行，不在 socket 的属组里。
// 把组名和修复命令直接写出来，用户不用再去猜是哪个组。
func PermissionHint() string {
	gid, ok := SocketGID()
	if !ok {
		return "没有权限访问 " + DefaultSocket + "：应用用户不在 docker 组（升级/重装本应用会自动加入，装完重启应用生效）"
	}
	grp := GroupName(gid)
	who := CurrentUserName()
	if grp == "root" {
		// 属组是 root 时 usermod 帮不上忙：只能改 socket 的属组，或者放弃 Docker 方式
		return fmt.Sprintf(
			"没有权限访问 %s，且它的属组是 root（gid=0）：应用用户 %s 无法通过加组获得权限。可执行 sudo chgrp docker %s 后重启 Docker 与本应用，或让应用的运行用户加入该组",
			DefaultSocket, who, DefaultSocket,
		)
	}
	return fmt.Sprintf(
		"没有权限访问 %s（socket 属组 %s / gid=%d，当前用户 %s）：把应用用户加入该组再重启本应用，例如 sudo usermod -aG %s %s",
		DefaultSocket, grp, gid, who, grp, who,
	)
}
