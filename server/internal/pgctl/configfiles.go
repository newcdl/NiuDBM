package pgctl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PostgreSQL 的配置文件与 MySQL 的 conf.d 完全不是一回事：
//
//   - postgresql.conf 在 PGDATA 里，是主配置（不是"片段"）；
//   - pg_hba.conf 决定谁能连（相当于 MySQL 的 user@host）；
//   - postgresql.auto.conf 是 ALTER SYSTEM 的落盘位置，手改会被下一次 ALTER SYSTEM 覆盖，
//     所以只读展示——但要能看见，否则用户永远不知道参数页到底写了什么。
//
// 数据目录可能在命名卷里，宿主上根本看不到文件，因此读写都走 docker exec，
// 而不是像 MySQL 那样直接读宿主上的 conf 目录。
const (
	// pgDataPath 是官方镜像的 PGDATA。镜像 entrypoint 会 chown 它，
	// 我们只在这个目录里读写文件，不碰数据文件。
	pgDataPath = "/var/lib/postgresql/data"

	configPostgresql = "postgresql.conf"
	configPgHBA      = "pg_hba.conf"
	configAutoConf   = "postgresql.auto.conf"

	// backupSuffix 用点号前缀，避免和 PG 自己的文件重名
	backupSuffix = ".niudbm.bak"
)

// ConfigFile 描述一个可以在界面上查看/编辑的配置文件。
type ConfigFile struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Writable bool   `json:"writable"`
	Note     string `json:"note,omitempty"`
}

// ConfigFiles 返回这个实例可编辑的配置清单（顺序即界面顺序）。
func (s Spec) ConfigFiles() []ConfigFile {
	return []ConfigFile{
		{
			Name: configPostgresql, Label: "postgresql.conf", Writable: true,
			Note: "主配置。改完执行 reload 即生效；标了 restart 的参数要重启服务",
		},
		{
			Name: configPgHBA, Label: "pg_hba.conf", Writable: true,
			Note: "访问控制：谁能从哪个地址、用什么方式连。改完 reload 即生效",
		},
		{
			Name: configAutoConf, Label: "postgresql.auto.conf", Writable: false,
			Note: "「配置」页写的参数会落在这里，由 ALTER SYSTEM 维护，不能手改",
		},
	}
}

// validConfigName 只允许白名单里的文件名：既挡路径穿越，也避免误改数据文件。
func validConfigName(name string) bool {
	switch name {
	case configPostgresql, configPgHBA, configAutoConf:
		return true
	}
	return false
}

// ReadConfigFile 读取配置文件原文。
//
// 容器没跑时退回读宿主目录（数据目录是绑挂的情况），否则直接说清"先启动"——
// 卷里的文件宿主看不见，硬读只会给一个 ENOENT 把用户带偏。
func (s Spec) ReadConfigFile(name string) (string, error) {
	if !validConfigName(name) {
		return "", fmt.Errorf("不支持的配置文件：%s", name)
	}
	if s.Running() {
		out, err := s.Docker.Client.Exec(context.Background(), s.Docker.Container, nil, nil,
			"sh", "-c", "cat "+pgDataPath+"/"+name)
		if err == nil {
			return out, nil
		}
		// 文件不存在（比如还没被 ALTER SYSTEM 写过）当空内容处理
		if strings.Contains(err.Error(), "No such file") {
			return "", nil
		}
		return "", err
	}
	// 绑挂目录时宿主上就是同一个文件，停着也能看
	if s.DataDir != "" {
		if raw, err := os.ReadFile(filepath.Join(s.DataDir, name)); err == nil {
			return string(raw), nil
		}
	}
	return "", errors.New("服务未运行：数据在 Docker 卷里，宿主上看不到；请先启动服务再看配置")
}

// WriteConfigFile 写入配置文件，然后 reload 并回报解析错误。
//
// 关键点：reload 失败不会杀掉正在跑的实例（PG 只会在日志里报错并继续用旧配置），
// 所以这里的顺序是"写 → reload → 查 pg_file_settings"，把写坏的行号原样告诉用户，
// 而不是等下次重启才发现服务起不来。
func (s Spec) WriteConfigFile(name, content string) ([]string, error) {
	files := s.ConfigFiles()
	writable := false
	for _, f := range files {
		if f.Name == name {
			writable = f.Writable
		}
	}
	if !validConfigName(name) {
		return nil, fmt.Errorf("不支持的配置文件：%s", name)
	}
	if !writable {
		return nil, fmt.Errorf("%s 由 ALTER SYSTEM 维护，不能在页面上直接改", name)
	}
	if !s.Running() {
		return nil, errors.New("服务未运行：配置文件在容器内，请先启动服务再修改")
	}
	if err := s.writeContainerFile(name, content, true); err != nil {
		return nil, err
	}
	return s.reloadAndCheck(name)
}

// RestoreConfigFile 用上一次保存前的备份覆盖回来。
func (s Spec) RestoreConfigFile(name string) ([]string, error) {
	if !validConfigName(name) {
		return nil, fmt.Errorf("不支持的配置文件：%s", name)
	}
	for _, f := range s.ConfigFiles() {
		if f.Name == name && !f.Writable {
			return nil, fmt.Errorf("%s 由 ALTER SYSTEM 维护，不能直接恢复", name)
		}
	}
	if !s.Running() {
		return nil, errors.New("服务未运行：配置文件在容器内，请先启动服务再恢复")
	}
	src := pgDataPath + "/" + name + backupSuffix
	if _, err := s.Docker.Client.Exec(context.Background(), s.Docker.Container, nil, nil,
		"sh", "-c", "test -f "+src); err != nil {
		return nil, errors.New("没有可恢复的上一版配置")
	}
	if _, err := s.Docker.Client.Exec(context.Background(), s.Docker.Container, nil, nil,
		"sh", "-c", "cp "+src+" "+pgDataPath+"/"+name+" && chown postgres:postgres "+pgDataPath+"/"+name); err != nil {
		return nil, err
	}
	return s.reloadAndCheck(name)
}

// HasBackup 判断容器内是否已经有上一版配置备份。
func (s Spec) HasBackup(name string) bool {
	if !validConfigName(name) || !s.Running() {
		return false
	}
	src := pgDataPath + "/" + name + backupSuffix
	_, err := s.Docker.Client.Exec(context.Background(), s.Docker.Container, nil, nil,
		"sh", "-c", "test -f "+src)
	return err == nil
}

// PendingRestart 读取 PG 自己的配置状态，判断刚 reload 后是否有参数必须重启。
func (s Spec) PendingRestart() bool {
	pending := false
	err := s.withConn(RootUser, func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx,
			`SELECT COALESCE(bool_or(pending_restart), false) FROM pg_settings`).Scan(&pending)
	})
	return err == nil && pending
}

// writeContainerFile 先把内容写进临时文件再 mv：直接截断写一旦中途失败，
// 留下半份配置文件比不写更糟（下次重启直接起不来）。
func (s Spec) writeContainerFile(name, content string, keepBackup bool) error {
	tmp := pgDataPath + "/." + name + ".new"
	dst := pgDataPath + "/" + name
	script := "cat > " + tmp + " && chown postgres:postgres " + tmp + " && chmod 600 " + tmp
	if keepBackup {
		// 第一次保存才留备份：之后反复保存不该把最初的版本冲掉
		script += " && if [ -f " + dst + " ] && [ ! -f " + dst + backupSuffix + " ]; then cp " + dst + " " + dst + backupSuffix + "; fi"
	}
	script += " && mv " + tmp + " " + dst
	if _, err := s.Docker.Client.Exec(context.Background(), s.Docker.Container, nil,
		io.NopCloser(strings.NewReader(content)), "sh", "-c", script); err != nil {
		return fmt.Errorf("写入 %s 失败：%w", name, err)
	}
	return nil
}

// reloadAndCheck 重载配置并回报解析错误。
func (s Spec) reloadAndCheck(name string) ([]string, error) {
	if err := s.reload(); err != nil {
		return nil, err
	}
	warnings, err := s.configFileErrors()
	if err != nil {
		// 查不到错误清单不算失败：配置已经写进去了，reload 也发过了
		return nil, nil
	}
	return warnings, nil
}

// reload 让 PG 重新读配置（pg_hba.conf 也走这一条）。
func (s Spec) reload() error {
	return s.withConn(RootUser, func(ctx context.Context, db *sql.DB) error {
		if _, err := db.ExecContext(ctx, `SELECT pg_reload_conf()`); err != nil {
			return fmt.Errorf("重载配置失败：%w", translate(err))
		}
		return nil
	})
}

// configFileErrors 返回配置文件里解析失败的行。
//
// pg_file_settings 是 PG 自己维护的"最后一次解析结果"，比我们猜要准：
// 哪一行、哪个参数、什么错都写在这里。
func (s Spec) configFileErrors() ([]string, error) {
	out := []string{}
	err := s.withConn(RootUser, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `
			SELECT COALESCE(sourcefile, ''), COALESCE(sourceline, 0), COALESCE(name, ''), error
			FROM pg_file_settings
			WHERE error IS NOT NULL
			ORDER BY seqno
			LIMIT 20`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var file, param, msg string
			var line int
			if err := rows.Scan(&file, &line, &param, &msg); err != nil {
				return err
			}
			// 行号比"哪一行猜的"有用得多：直接告诉用户去第几行改
			where := baseName(file)
			if line > 0 {
				where = fmt.Sprintf("%s 第 %d 行", where, line)
			}
			if param != "" {
				out = append(out, fmt.Sprintf("%s：%s —— %s", where, param, msg))
				continue
			}
			out = append(out, fmt.Sprintf("%s：%s", where, msg))
		}
		return rows.Err()
	})
	return out, err
}

func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}
