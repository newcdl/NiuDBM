package mysqlctl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// rootAccount 是 root 的固定来源：本机与任意地址。
const (
	rootLocal  = "localhost"
	rootRemote = "%"
)

// RootRemote 判断 root 是否允许从任意地址连接（root@'%' 是否存在）。
//
// 镜像初始化时按 MYSQL_ROOT_HOST 建了 root@'%'，但用户可以主动关掉它：
// 关掉之后只有容器内的 localhost 能连 root，外部必须先建业务账号。
func (s Spec) RootRemote() (bool, error) {
	db, err := s.open("")
	if err != nil {
		return false, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var n int
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM mysql.user WHERE User = 'root' AND Host = "+quoteLiteral(rootRemote)).Scan(&n)
	if err != nil {
		return false, translate(err)
	}
	return n > 0, nil
}

// SetRootRemote 打开或关闭 root 的远程访问。
//
// 打开时用现有 root 密码建一份 root@'%' 并给全权；关闭时直接删掉这条账号记录，
// root@localhost 不受影响，所以容器内的运维连接不会断。
func (s Spec) SetRootRemote(remote bool, password string) error {
	if remote && len(password) < 8 {
		return errors.New("开启远程访问需要先设置 root 密码（至少 8 位）")
	}
	return s.withConn(func(ctx context.Context, db *sql.DB) error {
		acct := account(SuperUserRoot, rootRemote)
		if !remote {
			if _, err := db.ExecContext(ctx, "DROP USER IF EXISTS "+acct); err != nil {
				return fmt.Errorf("关闭 root 远程访问失败：%w", translate(err))
			}
			return nil
		}
		if _, err := db.ExecContext(ctx,
			"CREATE USER IF NOT EXISTS "+acct+" IDENTIFIED BY "+quoteLiteral(password)); err != nil {
			return fmt.Errorf("创建 root 远程账号失败：%w", translate(err))
		}
		// 已存在时把密码对齐到当前 root 密码，避免两条记录的密码不一致
		if _, err := db.ExecContext(ctx, s.setPasswordSQL(acct, password)); err != nil {
			return fmt.Errorf("同步 root 密码失败：%w", translate(err))
		}
		if _, err := db.ExecContext(ctx, "GRANT ALL PRIVILEGES ON *.* TO "+acct+" WITH GRANT OPTION"); err != nil {
			return fmt.Errorf("授予 root 全权失败：%w", translate(err))
		}
		return nil
	})
}

// SetRootPassword 改 root 密码：本机与远程两个来源一起改，改完只有新密码能连。
func (s Spec) SetRootPassword(password string) error {
	if len(password) < 8 {
		return errors.New("密码至少 8 位")
	}
	return s.withConn(func(ctx context.Context, db *sql.DB) error {
		hosts, err := hostsOfRole(ctx, db, SuperUserRoot)
		if err != nil {
			return fmt.Errorf("读取 root 来源失败：%w", err)
		}
		if len(hosts) == 0 {
			hosts = []string{rootLocal}
		}
		for _, h := range hosts {
			if _, err := db.ExecContext(ctx, s.setPasswordSQL(account(SuperUserRoot, h), password)); err != nil {
				return fmt.Errorf("修改 root 密码失败（%s）：%w", h, translate(err))
			}
		}
		return nil
	})
}
