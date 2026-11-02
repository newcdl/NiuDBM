package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"niudbm/server/internal/store"
)

// 部署失败不能"收拾干净"。
//
// 之前的做法是失败就删容器、删目录、删密码，用户手里只剩一句"1m30s 内实例没就绪"：
// 容器已经被删掉，docker logs 无从查起，连是权限问题、镜像问题还是初始化太慢
// 都判不出来。现在的约定是：失败即保留现场——
//   - 容器只停不删（docker run 用的是 --restart no，不会自己再起来）
//   - 数据目录、命名卷、密码文件都留着
//   - 容器日志（或本机引擎日志）连同失败阶段写进 <实例目录>/deploy.log
//   - 台账状态置 failed 并写原因，列表里能看到这一行，可以看日志、删掉重来
const deployLogName = "deploy.log"

func (s *Server) deployLogPath(in store.Instance) string {
	return filepath.Join(s.instanceDir(in), deployLogName)
}

// markDeployFailed 记录失败现场并把实例标成 failed，reason 里给出日志位置。
func (s *Server) markDeployFailed(ctx context.Context, in store.Instance, stage, cause string) {
	logPath := s.deployLogPath(in)
	entry := s.collectDeployLog(ctx, in, stage, cause)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err == nil {
		// 追加而不是覆盖：重试过几次的用户需要看到每一次失败长什么样
		if f, ferr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); ferr == nil {
			_, _ = f.WriteString(entry + "\n")
			_ = f.Close()
		}
	}
	reason := truncate(cause, 240) + "；失败现场已保留，日志见 " + logPath
	_ = s.st.SetStatus(ctx, in.ID, "failed", reason)
}

// collectDeployLog 组装一段可读的失败记录：阶段、原因、引擎信息与日志尾部。
func (s *Server) collectDeployLog(ctx context.Context, in store.Instance, stage, cause string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] 阶段 %s 失败：%s\n", time.Now().Format("2006-01-02 15:04:05"), stage, cause)
	fmt.Fprintf(&b, "实例 %s（%s %s，%s，端口 %d）\n", in.Name, in.Engine, in.EngineVersion, in.Runtime, in.Port)

	if in.Runtime == runtimeDocker {
		name := containerNameOf(in)
		out, err := s.dockerLogTail(ctx, in, 300)
		switch {
		case err != nil:
			fmt.Fprintf(&b, "读取容器 %s 的日志失败：%v\n", name, err)
		case strings.TrimSpace(out) == "":
			fmt.Fprintf(&b, "容器 %s 没有输出日志（可能还没启动起来）\n", name)
			// 停在 created 和 exited 是完全不同的两种原因，补一行状态省得再问一轮
			if state := s.dockerClient().State(ctx, name); state != "" {
				fmt.Fprintf(&b, "容器状态：%s\n", state)
			}
		default:
			fmt.Fprintf(&b, "---- docker logs %s（最后 300 行）----\n%s\n", name, strings.TrimRight(out, "\n"))
		}
		return b.String()
	}

	// 只有容器方式：日志就是 docker logs
	return b.String()
}

// handleDeployLog 给界面读部署现场：优先落盘的 deploy.log，
// 没有时退回实时容器日志（正常建成的实例也可能想看日志）。
func (s *Server) handleDeployLog(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.require(w, r, true); !ok {
		return
	}
	in, ok := s.load(w, r)
	if !ok {
		return
	}
	logPath := s.deployLogPath(in)
	if buf, err := os.ReadFile(logPath); err == nil {
		writeOK(w, r, map[string]any{
			"available": true,
			"path":      logPath,
			"log":       headClip(string(buf), 200*1024),
		})
		return
	}
	if in.Runtime == runtimeDocker {
		if out, err := s.dockerLogTail(r.Context(), in, 300); err == nil && strings.TrimSpace(out) != "" {
			writeOK(w, r, map[string]any{
				"available": true,
				"path":      "docker logs " + containerNameOf(in),
				"log":       headClip(out, 200*1024),
			})
			return
		}
	}
	writeOK(w, r, map[string]any{
		"available": false,
		"path":      logPath,
		"log":       "",
		"message":   "还没有部署日志：这个实例不是部署失败留下的，或者日志已被清理",
	})
}

// headClip 限制返回长度：界面只显示尾部，不必把几十兆日志塞进 JSON。
func headClip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}
