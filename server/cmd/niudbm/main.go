// 牛库管家服务端入口。
//
// 运行方式：
//   - 平台内：由 cmd/main 拉起，监听 target/app.sock，由统一网关转发 /app/niudbm
//   - 开发：niudbm --addr 127.0.0.1:8780
//   - 诊断：niudbm --diag（SSH 场景，不需要界面）
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"niudbm/server/internal/diag"
	"niudbm/server/internal/httpapi"
	"niudbm/server/internal/platform"
	"niudbm/server/internal/store"
)

// version 由构建脚本通过 -ldflags 注入，默认值便于 go run 时区分。
var version = "1.0.0"

func main() {
	socketPath := flag.String("socket", "", "Unix socket 路径（网关模式）")
	addr := flag.String("addr", "", "TCP 监听地址（开发模式），如 127.0.0.1:8780")
	prefix := flag.String("prefix", "", "网关访问前缀，默认 /app/<appname>")
	runDiag := flag.Bool("diag", false, "输出平台自检结果后退出")
	showVersion := flag.Bool("version", false, "输出版本号后退出")
	flag.Parse()

	env := platform.Load(version)

	if *showVersion {
		fmt.Println(version)
		return
	}
	if *runDiag {
		diag.PrintText(diag.Run(env))
		return
	}

	paths, err := preparePaths(env)
	if err != nil {
		log.Fatalf("准备目录失败: %v", err)
	}

	st, err := store.Open(paths.Meta)
	if err != nil {
		log.Fatalf("打开元数据库失败 %s: %v", paths.Meta, err)
	}
	defer st.Close()

	// 过期会话启动时清一次：GetSession 会顺手删自己那条，
	// 但「退出后再没回来过」的会话只能靠这里收尾。
	if purgeErr := st.PurgeSessions(context.Background()); purgeErr != nil {
		log.Printf("清理过期登录会话失败（不影响启动）: %v", purgeErr)
	}

	if *socketPath == "" && *addr == "" {
		if env.InPlatform {
			*socketPath = filepath.Join(env.AppDest, "app.sock")
		} else {
			*addr = "127.0.0.1:8780"
		}
	}
	if *prefix == "" && env.InPlatform {
		*prefix = "/app/" + env.AppName
	}

	handler := httpapi.New(env, *prefix, st, paths)

	// 容器状态与台账在平台重启后可能不一致（例如部署到一半被重启），
	// 启动时对齐一遍。放到后台做：容器初始化慢也不能堵住 socket 监听，
	// 否则网关会认为应用没起来。
	var bg sync.WaitGroup
	bg.Add(1)
	go func() {
		defer bg.Done()
		handler.Reconcile(context.Background())
	}()

	// 定时备份与巡检：到期备份、保留清理、实例掉线通知都靠这个循环。
	// 生命周期跟进程走，退出时先取消，避免关闭过程中又启动新的备份。
	schedCtx, schedCancel := context.WithCancel(context.Background())
	defer schedCancel()
	handler.StartScheduler(schedCtx)

	var (
		ln   net.Listener
		info string
	)
	if *socketPath != "" {
		if mkErr := os.MkdirAll(filepath.Dir(*socketPath), 0o755); mkErr != nil {
			log.Fatalf("创建 socket 目录失败: %v", mkErr)
		}
		// 上次异常退出可能留下 socket 文件，先清理再监听
		_ = os.Remove(*socketPath)
		ln, err = net.Listen("unix", *socketPath)
		if err != nil {
			log.Fatalf("监听 unix socket 失败 %s: %v", *socketPath, err)
		}
		_ = os.Chmod(*socketPath, 0o660)
		info = "unix:" + *socketPath
	} else {
		ln, err = net.Listen("tcp", *addr)
		if err != nil {
			log.Fatalf("监听 %s 失败: %v", *addr, err)
		}
		info = "tcp:" + *addr
	}
	handler.SetListen(info)

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("收到信号 %s，开始关闭", sig)
		schedCancel()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		// 先等自动拉起收尾，避免“刚起来就被停掉”留下半启动的实例
		bg.Wait()
		if shutErr := srv.Shutdown(ctx); shutErr != nil {
			log.Printf("关闭超时，强制退出: %v", shutErr)
		}
		// 数据库进程是我们拉起的，不主动停就会变成孤儿进程占着端口
		handler.Shutdown(ctx)
	}()

	// 平台内才写心跳：供自检判断服务是否真的活着（P-21）
	if env.InPlatform {
		go writeHeartbeat(env.PkgVar)
	}

	log.Printf("niudbm %s 监听 %s（前缀 %q）", version, info, *prefix)
	log.Printf("实例目录 %s ｜ 备份额 %s", paths.Instances, paths.Backups)
	if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		log.Fatalf("服务异常退出: %v", serveErr)
	}

	// 正常停止时清掉 socket，避免下次启动被残留文件挡住
	if *socketPath != "" {
		_ = os.Remove(*socketPath)
	}
	log.Println("已停止")
}

// preparePaths 算出并创建全部落盘目录。
//
// 备份额优先放到平台分配的共享目录（用户能在文件管理器里直接看到、能随快照备份），
// 共享目录不可用时回退到应用私有 var 下，绝不因为目录问题让服务起不来。
func preparePaths(env platform.Env) (httpapi.Paths, error) {
	instances := filepath.Join(env.PkgVar, "instances")
	trash := filepath.Join(env.PkgVar, "trash")
	// 实例密码单独放 etc 下且不给组外读权限：备份目录是共享目录，密码绝不能落在那里
	secrets := filepath.Join(env.PkgEtc, "secrets")
	meta := filepath.Join(env.PkgVar, "niudbm.db")

	backups := filepath.Join(env.PkgVar, "backups")
	for _, share := range env.DataSharePaths {
		if !strings.Contains(filepath.Base(share), "backup") {
			continue
		}
		if err := os.MkdirAll(share, 0o750); err != nil {
			continue
		}
		// MkdirAll 对已存在但只读的目录也会返回 nil，必须真写一次才知道能不能用。
		// 共享目录属主是平台给的，写不进去时默默回退到私有目录，比启动失败强。
		probe := filepath.Join(share, ".niudbm-probe")
		f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			continue
		}
		_ = f.Close()
		_ = os.Remove(probe)
		backups = share
		break
	}

	for _, dir := range []string{instances, backups, trash, secrets, filepath.Dir(meta)} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return httpapi.Paths{}, fmt.Errorf("%s: %w", dir, err)
		}
	}
	// 实例根目录要可被引擎身份穿过：服务以 root 跑时，库进程是降权运行的，
	// 父目录若 0750 root:root，它连自己的数据目录都进不去。
	_ = os.Chmod(instances, 0o755)
	_ = os.Chmod(secrets, 0o700)
	return httpapi.Paths{Instances: instances, Backups: backups, Trash: trash, Secrets: secrets, Meta: meta}, nil
}

// writeHeartbeat 每 30 秒刷新一次心跳文件，进程被杀后心跳会自然停住。
func writeHeartbeat(dir string) {
	path := filepath.Join(dir, "heartbeat")
	for {
		_ = os.WriteFile(path, []byte(time.Now().Format("2006-01-02 15:04:05")), 0o644)
		time.Sleep(30 * time.Second)
	}
}
