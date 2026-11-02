package mysqlctl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RoleGrant 是一个账号在某个库上的授权：档位与库一一对应。
//
// 一个账号可以拿到多个库的授权（应用连一个库、报表连另一个库），
// 所以授权不能只是一个 level 字段——那样只能表达"在某个默认库上的权限"。
type RoleGrant struct {
	Database string `json:"database"`
	Level    string `json:"level"`
}

// Role 是一个数据库用户。
//
// 一个用户名可以对应多条 MySQL 账号记录（user@'%'、user@'172.16.10.111'），
// 引擎层面它们是彼此独立的账号；界面上按用户名聚合成一个"用户"，访问来源放在 Hosts 里。
type Role struct {
	Name string `json:"name"`
	// Login 恒为 true：MySQL 的用户天然可以登录
	Login bool `json:"login"`
	// Super 是超级用户，界面上不允许改权限或删除
	Super bool `json:"super"`
	// Level 是为兼容旧界面保留的：取第一个授权库的档位。
	// 新界面看 Grants，一个账号可能同时有权访问好几个库。
	Level string `json:"level"`
	// System 标记引擎自带的账号（界面只读）
	System bool `json:"system"`
	// Grants 是这个账号当前的库授权列表
	Grants []RoleGrant `json:"grants"`
	// Hosts 是允许来源：["%"] 表示所有人，具体 IP 表示只允许这些地址
	Hosts []string `json:"hosts"`
}

// 权限档位。与 PostgreSQL 保持一致，界面不用区分引擎。
const (
	LevelNone  = "none"
	LevelRead  = "read"
	LevelWrite = "write"
	LevelAll   = "all"
)

var roleNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,31}$`)

// reservedRoles 是建用户时不能碰的名字。
var reservedRoles = map[string]bool{
	"root": true, "niudbm": true, "mysql": true, "mariadb": true,
	"mariadb.sys": true, "mysql.sys": true, "mysql.session": true,
	"mysql.infoschema": true, "public": true, "admin": true,
}

// systemUsers 是不在用户管理里展示的引擎账号。
// mysql.infoschema 是 MySQL 8.0+ 自带的账号，误删会让 information_schema 出问题。
var systemUsers = map[string]bool{
	"root": true, "niudbm": true, "mysql": true, "mariadb": true,
	"mariadb.sys": true, "mysql.sys": true, "mysql.session": true,
	"mysql.infoschema": true, "debian-sys-maint": true,
}

// ValidRoleName 校验新建用户的名称。
func ValidRoleName(name string) error {
	if !roleNamePattern.MatchString(name) {
		return errors.New("用户名只能包含字母、数字、下划线，且以字母或下划线开头（最长 32）")
	}
	if reservedRoles[strings.ToLower(name)] {
		return fmt.Errorf("%s 是保留名称，请换一个", name)
	}
	return nil
}

// ValidHost 校验允许来源。
//
// 接受四种形态：% 表示所有人、localhost、单个 IPv4、以及网段
// （172.16.10.0/24 或 172.16.10.0/255.255.255.0）。
// 不放开任意主机名：主机名要反查 DNS，而 MySQL 默认的 skip-name-resolve 下不生效，
// 写上去看着能用、实际连不上。
var hostPattern = regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}(/(\d{1,2}|(\d{1,3}\.){3}\d{1,3}))?$`)

func ValidHost(host string) error {
	h := strings.TrimSpace(host)
	if h == "" {
		return errors.New("访问来源不能为空")
	}
	if h == "%" || h == "localhost" || hostPattern.MatchString(h) {
		if h != "%" && h != "localhost" && strings.Contains(h, "/") {
			if _, err := netmaskOf(h); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("访问来源 %s 不合法：填 %% 表示所有人，或填 IPv4 / 网段（如 172.16.10.0/24）", h)
}

// netmaskOf 把 172.16.10.0/24 这种前缀长度写法换成 MySQL 认的点分掩码。
//
// MySQL 的 Host 列不认 CIDR 前缀：写成 172.16.10.0/24 会被当成主机名匹配不上，
// 只有 172.16.10.0/255.255.255.0 这种形式才按网段匹配。
func netmaskOf(host string) (string, error) {
	idx := strings.Index(host, "/")
	ip, mask := host[:idx], host[idx+1:]
	if strings.Contains(mask, ".") {
		return host, nil
	}
	bits, err := strconv.Atoi(mask)
	if err != nil || bits < 0 || bits > 32 {
		return "", fmt.Errorf("网段 %s 的掩码不合法，应为 0~32 或 255.255.255.0 这种写法", host)
	}
	var v uint32
	if bits > 0 {
		v = ^uint32(0) << (32 - bits)
	}
	dotted := fmt.Sprintf("%d.%d.%d.%d", byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	return ip + "/" + dotted, nil
}

// NormalizeHosts 去重、去空白、把 CIDR 前缀换成点分掩码，并给出默认值。
func NormalizeHosts(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	seen := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		v := strings.TrimSpace(h)
		if v == "" {
			continue
		}
		if strings.Contains(v, "/") {
			if converted, err := netmaskOf(v); err == nil {
				v = converted
			}
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	if len(out) == 0 {
		out = []string{"%"}
	}
	return out
}

// ValidLevel 校验权限档位。
func ValidLevel(level string) bool {
	switch level {
	case LevelNone, LevelRead, LevelWrite, LevelAll:
		return true
	}
	return false
}

// Roles 列出所有业务账号及其库授权。
func (s Spec) Roles() ([]Role, error) {
	db, err := s.open("")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, "SELECT User, Host FROM mysql.user ORDER BY User")
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()

	type account struct{ user, host string }
	var accounts []account
	for rows.Next() {
		var a account
		if err := rows.Scan(&a.user, &a.host); err != nil {
			return nil, translate(err)
		}
		if systemUsers[strings.ToLower(a.user)] {
			continue
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, translate(err)
	}

	// 同一个用户名可能有多条账号记录（user@'%'、user@'172.16.10.111'）：
	// 界面上把它们当一个用户看，来源与授权分别合并。
	order := make([]string, 0, len(accounts))
	hostsOf := make(map[string][]string, len(accounts))
	grantsOf := make(map[string]map[string]string, len(accounts))
	grantOrder := make(map[string][]string, len(accounts))
	for _, a := range accounts {
		if _, ok := hostsOf[a.user]; !ok {
			order = append(order, a.user)
			grantsOf[a.user] = map[string]string{}
		}
		hostsOf[a.user] = append(hostsOf[a.user], a.host)
		for _, g := range s.roleGrants(ctx, db, a.user, a.host) {
			key := strings.ToLower(g.Database)
			prev, seen := grantsOf[a.user][key]
			if !seen {
				grantOrder[a.user] = append(grantOrder[a.user], g.Database)
				grantsOf[a.user][key] = g.Level
				continue
			}
			if levelRank(g.Level) > levelRank(prev) {
				grantsOf[a.user][key] = g.Level
			}
		}
	}

	out := make([]Role, 0, len(order))
	for _, name := range order {
		grants := make([]RoleGrant, 0, len(grantOrder[name]))
		for _, dbName := range grantOrder[name] {
			grants = append(grants, RoleGrant{Database: dbName, Level: grantsOf[name][strings.ToLower(dbName)]})
		}
		r := Role{
			Name:   name,
			Login:  true,
			Super:  false,
			System: false,
			Level:  LevelNone,
			Grants: grants,
			Hosts:  hostsOf[name],
		}
		if len(grants) > 0 {
			r.Level = grants[0].Level
		}
		out = append(out, r)
	}
	return out, nil
}

// hostsOfRole 读出这个用户名当前在引擎里的全部来源。
func hostsOfRole(ctx context.Context, db *sql.DB, name string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT Host FROM mysql.user WHERE User = "+quoteLiteral(name)+" ORDER BY Host")
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	var hosts []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, translate(err)
		}
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
}

// roleGrants 从 SHOW GRANTS 反推每个库上的档位。
// 判断依据是真实权限，而不是我们记过的意图：用户在客户端里手工改过权限也能读出来。
func (s Spec) roleGrants(ctx context.Context, db *sql.DB, user, host string) []RoleGrant {
	rows, err := db.QueryContext(ctx, "SHOW GRANTS FOR "+quoteLiteral(user)+"@"+quoteLiteral(host))
	if err != nil {
		return nil
	}
	defer rows.Close()

	byDB := make(map[string]string, 4)
	order := make([]string, 0, 4)
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			continue
		}
		name, level, ok := parseGrantLine(line)
		if !ok {
			continue
		}
		prev, seen := byDB[name]
		if !seen {
			order = append(order, name)
			byDB[name] = level
			continue
		}
		// 同一个库可能出现在多行（先 SELECT 再 CREATE），取更高的档位
		if levelRank(level) > levelRank(prev) {
			byDB[name] = level
		}
	}
	out := make([]RoleGrant, 0, len(order))
	for _, name := range order {
		out = append(out, RoleGrant{Database: name, Level: byDB[name]})
	}
	return out
}

// parseGrantLine 从一行 GRANT 里取出库名与档位。
//
// 形如：GRANT SELECT, INSERT ON `shop`.* TO 'app'@'%'
// 全局授权（ON *.*）与 USAGE 这类"没有任何库权限"的行都跳过。
func parseGrantLine(line string) (string, string, bool) {
	upper := strings.ToUpper(line)
	onIdx := strings.Index(upper, " ON ")
	toIdx := strings.Index(upper, " TO ")
	if onIdx < 0 || toIdx <= onIdx {
		return "", "", false
	}
	scope := strings.TrimSpace(line[onIdx+4 : toIdx])
	if !strings.HasSuffix(scope, ".*") {
		// 表级授权或 *.* 全局授权：不归到某个库上
		return "", "", false
	}
	dbName := strings.Trim(strings.TrimSuffix(scope, ".*"), "`")
	if dbName == "" || dbName == "*" || systemSchemas[strings.ToLower(dbName)] {
		return "", "", false
	}
	level := grantLevelOf(upper[:onIdx])
	if level == LevelNone {
		return "", "", false
	}
	return dbName, level, true
}

// grantLevelOf 把 GRANT 后面的权限列表折算成档位。
func grantLevelOf(privs string) string {
	switch {
	case strings.Contains(privs, "ALL PRIVILEGES"):
		return LevelAll
	case strings.Contains(privs, "INSERT"), strings.Contains(privs, "UPDATE"), strings.Contains(privs, "DELETE"):
		return LevelWrite
	case strings.Contains(privs, "SELECT"):
		return LevelRead
	}
	return LevelNone
}

func levelRank(level string) int {
	switch level {
	case LevelAll:
		return 3
	case LevelWrite:
		return 2
	case LevelRead:
		return 1
	}
	return 0
}

// account 拼出 MySQL 的账号字面量：'name'@'host'。
func account(name, host string) string {
	return quoteLiteral(name) + "@" + quoteLiteral(host)
}

// CreateRole 建一个可登录的账号，并可选地一次授好若干库。
//
// grants 可以为空：服务里可能一个库都还没有，先把账号建好、之后按需授权同样是常见做法。
// hosts 为空时按"所有人（%）"处理；填了具体 IP 就只为这些来源建账号。
func (s Spec) CreateRole(name string, hosts []string, password string, grants []RoleGrant) error {
	if err := ValidRoleName(name); err != nil {
		return err
	}
	if err := validGrants(grants); err != nil {
		return err
	}
	if len(password) < 8 {
		return errors.New("密码至少 8 位")
	}
	hosts = NormalizeHosts(hosts)
	for _, h := range hosts {
		if err := ValidHost(h); err != nil {
			return err
		}
	}
	return s.withConn(func(ctx context.Context, db *sql.DB) error {
		created := make([]string, 0, len(hosts))
		rollback := func() {
			for _, acct := range created {
				_, _ = db.ExecContext(ctx, "DROP USER "+acct)
			}
		}
		for _, h := range hosts {
			acct := account(name, h)
			if _, err := db.ExecContext(ctx, "CREATE USER "+acct+" IDENTIFIED BY "+quoteLiteral(password)); err != nil {
				rollback()
				return fmt.Errorf("创建用户失败：%w", translate(err))
			}
			created = append(created, acct)
			// 建用户成功但授权失败时也要收拾干净，避免留下一个半成品账号
			if err := s.applyGrants(ctx, db, name, h, grants); err != nil {
				rollback()
				return err
			}
		}
		return nil
	})
}

// CreateRoleOn 建账号并只把它授权到指定库上（一个账号只管一个库的快捷路径）。
func (s Spec) CreateRoleOn(dbName, name string, hosts []string, password, level string) error {
	return s.CreateRole(name, hosts, password, []RoleGrant{{Database: dbName, Level: level}})
}

// UpdateRole 改密码、来源与库授权。
//
// 三个参数都是"给什么用什么"：hosts 为 nil 表示不动来源，非 nil 表示以这份为准
// （新增的建账号、去掉的删账号）；grants 同理，传空切片表示撤销全部授权。
func (s Spec) UpdateRole(name string, hosts []string, password string, grants []RoleGrant) error {
	if err := ValidRoleName(name); err != nil {
		return err
	}
	if password == "" && grants == nil && hosts == nil {
		return errors.New("没有需要修改的内容")
	}
	if password != "" && len(password) < 8 {
		return errors.New("密码至少 8 位")
	}
	if grants != nil {
		if err := validGrants(grants); err != nil {
			return err
		}
	}
	var want []string
	if hosts != nil {
		want = NormalizeHosts(hosts)
		for _, h := range want {
			if err := ValidHost(h); err != nil {
				return err
			}
		}
	}

	return s.withConn(func(ctx context.Context, db *sql.DB) error {
		if err := mutableRole(ctx, db, name); err != nil {
			return err
		}
		current, err := hostsOfRole(ctx, db, name)
		if err != nil {
			return fmt.Errorf("读取账号来源失败：%w", err)
		}
		keep := map[string]bool{}
		if want != nil {
			keepSet := map[string]bool{}
			for _, h := range want {
				keepSet[h] = true
			}
			// 去掉的来源：直接删账号（同名的其它来源不受影响）
			for _, h := range current {
				if keepSet[h] {
					keep[h] = true
					continue
				}
				if _, err := db.ExecContext(ctx, "DROP USER "+account(name, h)); err != nil {
					return fmt.Errorf("移除来源 %s 失败：%w", h, translate(err))
				}
			}
			// 新增的来源：建账号，密码与现有一致
			for _, h := range want {
				if keep[h] {
					continue
				}
				acct := account(name, h)
				if _, err := db.ExecContext(ctx, "CREATE USER "+acct+" IDENTIFIED BY "+quoteLiteral(password)); err != nil {
					return fmt.Errorf("新增来源 %s 失败（需要同时填密码）：%w", h, translate(err))
				}
				keep[h] = true
			}
		} else {
			for _, h := range current {
				keep[h] = true
			}
		}

		for h := range keep {
			acct := account(name, h)
			if password != "" {
				if _, err := db.ExecContext(ctx, s.setPasswordSQL(acct, password)); err != nil {
					return fmt.Errorf("修改密码失败：%w", translate(err))
				}
			}
			if grants != nil {
				if err := s.applyGrants(ctx, db, name, h, grants); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// DropRole 删除账号（用户名下的所有来源一起删）。
func (s Spec) DropRole(name string) error {
	if err := ValidRoleName(name); err != nil {
		return err
	}
	return s.withConn(func(ctx context.Context, db *sql.DB) error {
		if err := mutableRole(ctx, db, name); err != nil {
			return err
		}
		hosts, err := hostsOfRole(ctx, db, name)
		if err != nil {
			return err
		}
		for _, h := range hosts {
			if _, err := db.ExecContext(ctx, "DROP USER "+account(name, h)); err != nil {
				return fmt.Errorf("删除用户失败：%w", translate(err))
			}
		}
		return nil
	})
}

// applyGrants 把账号的库授权改成给定集合。
//
// 必须"以这份为准"：只补授不减授的话，降级（读写 → 只读）和撤销某个库都不会真的生效。
// 回收按库做（ON `db`.*），不动 *.* 全局权限——全局权限里含账号的登录能力，
// 一并收掉会把账号变成连不上（以前就是这么写，多库场景下还会误伤别的库）。
func (s Spec) applyGrants(ctx context.Context, db *sql.DB, name, host string, want []RoleGrant) error {
	acct := account(name, host)

	wanted := make(map[string]string, len(want))
	for _, g := range want {
		wanted[strings.ToLower(strings.TrimSpace(g.Database))] = g.Level
	}
	// 先撤掉不在集合里的库
	for _, g := range s.roleGrants(ctx, db, name, host) {
		if _, keep := wanted[strings.ToLower(g.Database)]; keep {
			continue
		}
		stmt := "REVOKE ALL PRIVILEGES ON " + quoteIdent(g.Database) + ".* FROM " + acct
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("撤销 %s 上的授权失败：%w", g.Database, translate(err))
		}
	}

	for _, g := range want {
		dbName := strings.TrimSpace(g.Database)
		dbIdent := quoteIdent(dbName)
		// 先收再授：不然把 all 降到 read 时，建表权限会留在账号上
		if _, err := db.ExecContext(ctx, "REVOKE ALL PRIVILEGES ON "+dbIdent+".* FROM "+acct); err != nil {
			// 本来就没有该库的权限时 REVOKE 会报错，这属于正常情况
			_ = err
		}
		if g.Level == LevelNone {
			continue
		}
		grant := "SELECT"
		if g.Level == LevelWrite || g.Level == LevelAll {
			grant = "SELECT, INSERT, UPDATE, DELETE"
		}
		if _, err := db.ExecContext(ctx, "GRANT "+grant+" ON "+dbIdent+".* TO "+acct); err != nil {
			return fmt.Errorf("授权 %s 失败：%w", dbName, translate(err))
		}
		if g.Level == LevelAll {
			// all 额外给建表、改表、删除表这类结构权限
			if _, err := db.ExecContext(ctx,
				"GRANT CREATE, ALTER, DROP, INDEX, REFERENCES, CREATE TEMPORARY TABLES ON "+dbIdent+".* TO "+acct); err != nil {
				return fmt.Errorf("授权 %s 失败：%w", dbName, translate(err))
			}
		}
	}
	return nil
}

// validGrants 校验一批库授权。
func validGrants(grants []RoleGrant) error {
	seen := make(map[string]bool, len(grants))
	for _, g := range grants {
		name := strings.TrimSpace(g.Database)
		if err := ValidDBName(name); err != nil {
			return fmt.Errorf("库名不合法：%w", err)
		}
		if seen[strings.ToLower(name)] {
			return fmt.Errorf("库 %s 重复授权", name)
		}
		seen[strings.ToLower(name)] = true
		if !ValidLevel(g.Level) {
			return errors.New("权限档位只能是 read / write / all / none")
		}
	}
	return nil
}

// mutableRole 保护所有系统账号。
func mutableRole(ctx context.Context, db *sql.DB, name string) error {
	if systemUsers[strings.ToLower(name)] || reservedRoles[strings.ToLower(name)] {
		return errors.New("系统账号不能在用户管理中修改或删除")
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM mysql.user WHERE User = ?", name).Scan(&n); err != nil {
		return errors.New("用户不存在或无法读取")
	}
	if n == 0 {
		return errors.New("用户不存在")
	}
	return nil
}

// withConn 用管理账号连一个免库会话执行 DDL。
func (s Spec) withConn(fn func(ctx context.Context, db *sql.DB) error) error {
	db, err := s.open("")
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return fn(ctx, db)
}
