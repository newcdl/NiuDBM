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
	"syscall"
	"time"

	"niudbm/server/internal/diag"
	"niudbm/server/internal/httpapi"
	"niudbm/server/internal/platform"
)

// version 由构建脚本通过 -ldflags 注入，默认值便于 go run 时区分。
var version = "0.1.0-dev"

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

	handler := httpapi.New(env, *prefix)

	var (
		ln   net.Listener
		err  error
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
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if shutErr := srv.Shutdown(ctx); shutErr != nil {
			log.Printf("关闭超时，强制退出: %v", shutErr)
		}
	}()

	// 平台内才写心跳：供自检判断服务是否真的活着（P-21）
	if env.InPlatform {
		go writeHeartbeat(env.PkgVar)
	}

	log.Printf("niudbm %s 监听 %s（前缀 %q）", version, info, *prefix)
	if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		log.Fatalf("服务异常退出: %v", serveErr)
	}

	// 正常停止时清掉 socket，避免下次启动被残留文件挡住
	if *socketPath != "" {
		_ = os.Remove(*socketPath)
	}
	log.Println("已停止")
}

// writeHeartbeat 每 30 秒刷新一次心跳文件，进程被杀后心跳会自然停住。
func writeHeartbeat(dir string) {
	path := filepath.Join(dir, "heartbeat")
	for {
		_ = os.WriteFile(path, []byte(time.Now().Format("2006-01-02 15:04:05")), 0o644)
		time.Sleep(30 * time.Second)
	}
}
