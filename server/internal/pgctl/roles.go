package pgctl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"niudbm/server/internal/mysqlctl"
)

// systemRoles 是不在用户管理里展示、也不允许改动的账号。
//
// PG 的系统角色以 pg_ 开头，另有镜像自带的 postgres 与本应用建的 niudbm；
// 它们都拥有超出普通业务的权限，误删会让实例或本应用直接不可用。
var systemRoles = map[string]bool{
	"postgres": true, "niudbm": true,
}

// Roles 列出所有可登录账号及其库授权。
//
// PG 没有 user@host：一个角色就是一条记录，来源由 pg_hba.conf 决定，
// 因此 Hosts 恒为空，前端在 PG 下不展示这一列。
func (s Spec) Roles() ([]mysqlctl.Role, error) {
	db, err := s.open(RootUser)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx,
		`SELECT rolname FROM pg_roles
		 WHERE rolcanlogin AND NOT rolsuper AND rolname NOT LIKE 'pg\_%'
		 ORDER BY rolname`)
	if err != nil {
		db.Close()
		return nil, translate(err)
	}
	names := make([]string, 0, 8)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			db.Close()
			return nil, translate(err)
		}
		if systemRoles[strings.ToLower(name)] {
			continue
		}
		names = append(names, name)
	}
	rows.Close()
	db.Close()
	if err := rows.Err(); err != nil {
		return nil, translate(err)
	}

	// 每个库单独连一次，把"这个角色在这个库上的档位"读出来。
	// 库数量在 NAS 场景下很小，换来的是"权限以引擎为准"而不是靠我们记的意图。
	grantedBy := make(map[string][]mysqlctl.RoleGrant, len(names))
	for _, name := range names {
		grantedBy[name] = s.grantsOf(name)
	}

	out := make([]mysqlctl.Role, 0, len(names))
	for _, name := range names {
		grants := grantedBy[name]
		level := mysqlctl.LevelNone
		if len(grants) > 0 {
			level = grants[0].Level
		}
		out = append(out, mysqlctl.Role{
			Name:   name,
			Login:  true,
			Super:  false,
			System: false,
			Level:  level,
			Grants: grants,
		})
	}
	return out, nil
}

// grantsOf 读出某个角色在每个库上的档位。
//
// 判据全部来自引擎：CONNECT 表示"被授权过这个库"，schema CREATE 说明是全权，
// 表上有写权限是读写，只有读权限是只读。
func (s Spec) grantsOf(role string) []mysqlctl.RoleGrant {
	dbs, err := s.listDatabaseNames()
	if err != nil {
		return nil
	}
	out := make([]mysqlctl.RoleGrant, 0, len(dbs))
	for _, name := range dbs {
		level := s.levelInDatabase(role, name)
		if level == mysqlctl.LevelNone {
			continue
		}
		out = append(out, mysqlctl.RoleGrant{Database: name, Level: level})
	}
	return out
}

// listDatabaseNames 只取库名，避免 grantsOf 与 ListDatabases 互相递归。
func (s Spec) listDatabaseNames() ([]string, error) {
	db, err := s.open(RootUser)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx,
		`SELECT datname FROM pg_database WHERE datallowconn AND NOT datistemplate ORDER BY datname`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := make([]string, 0, 4)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, translate(err)
		}
		if systemDatabases[strings.ToLower(name)] {
			continue
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// levelInDatabase 判断角色在某个库上的档位。
func (s Spec) levelInDatabase(role, dbName string) string {
	db, err := s.open(dbName)
	if err != nil {
		return mysqlctl.LevelNone
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var connect, create, read, write, defaultWrite bool
	err = db.QueryRowContext(ctx, `
		SELECT
			has_database_privilege($1, current_database(), 'CONNECT'),
			has_schema_privilege($1, 'public', 'CREATE'),
			COALESCE((SELECT bool_or(has_table_privilege($1, c.oid, 'SELECT'))
			          FROM pg_class c
			          WHERE c.relkind = 'r'
			            AND c.relnamespace = (SELECT oid FROM pg_namespace WHERE nspname = 'public')), false),
			COALESCE((SELECT bool_or(has_table_privilege($1, c.oid, 'INSERT'))
			          FROM pg_class c
			          WHERE c.relkind = 'r'
			            AND c.relnamespace = (SELECT oid FROM pg_namespace WHERE nspname = 'public')), false),
			COALESCE((SELECT bool_or(acl.privilege_type IN ('INSERT', 'UPDATE', 'DELETE'))
			          FROM pg_default_acl d
			          CROSS JOIN LATERAL aclexplode(d.defaclacl) acl
			          WHERE d.defaclnamespace = (SELECT oid FROM pg_namespace WHERE nspname = 'public')
			            AND d.defaclobjtype = 'r'
			            AND acl.grantee = (SELECT oid FROM pg_roles WHERE rolname = $1)), false)`,
		role).Scan(&connect, &create, &read, &write, &defaultWrite)
	if err != nil {
		return mysqlctl.LevelNone
	}
	// 空库里没有表可查，此时"以后建的表会被授到什么权限"才代表档位；
	// 只看表权限会把刚授成 write 的账号显示成 read。
	write = write || (read == false && defaultWrite)

	switch {
	case !connect:
		return mysqlctl.LevelNone
	case create:
		return mysqlctl.LevelAll
	case write:
		return mysqlctl.LevelWrite
	case read:
		return mysqlctl.LevelRead
	default:
		// 能连、库里也没表、默认权限也读不出来：按只读呈现，
		// 用户重新选一次档位即可纠正，比凭空显示读写稳。
		return mysqlctl.LevelRead
	}
}

// CreateRole 建一个可登录账号，并可选地一次授好若干库。
func (s Spec) CreateRole(name string, hosts []string, password string, grants []mysqlctl.RoleGrant) error {
	if err := mysqlctl.ValidRoleName(name); err != nil {
		return err
	}
	if len(password) < 8 {
		return errors.New("密码至少 8 位")
	}
	if err := s.withConn(RootUser, func(ctx context.Context, db *sql.DB) error {
		_, err := db.ExecContext(ctx, "CREATE ROLE "+quoteIdent(name)+" WITH LOGIN PASSWORD "+quoteLiteral(password))
		if err != nil {
			return fmt.Errorf("创建用户失败：%w", translate(err))
		}
		return nil
	}); err != nil {
		return err
	}
	if len(grants) == 0 {
		return nil
	}
	if err := s.applyGrants(name, grants); err != nil {
		// 授权失败时把账号删掉，避免留下一个半成品
		_ = s.DropRole(name)
		return err
	}
	return nil
}

// CreateRoleOn 建账号并只把它授权到指定库上。
func (s Spec) CreateRoleOn(dbName, name string, hosts []string, password, level string) error {
	return s.CreateRole(name, hosts, password, []mysqlctl.RoleGrant{{Database: dbName, Level: level}})
}

// UpdateRole 改密码与库授权。
//
// hosts 在 PG 下没有意义（来源由 pg_hba.conf 决定），传什么都忽略。
func (s Spec) UpdateRole(name string, hosts []string, password string, grants []mysqlctl.RoleGrant) error {
	if err := mysqlctl.ValidRoleName(name); err != nil {
		return err
	}
	if password == "" && grants == nil {
		return errors.New("没有需要修改的内容")
	}
	if password != "" && len(password) < 8 {
		return errors.New("密码至少 8 位")
	}
	if systemRoles[strings.ToLower(name)] {
		return errors.New("系统账号不能在用户管理中修改")
	}
	if password != "" {
		if err := s.withConn(RootUser, func(ctx context.Context, db *sql.DB) error {
			if _, err := db.ExecContext(ctx,
				"ALTER ROLE "+quoteIdent(name)+" WITH PASSWORD "+quoteLiteral(password)); err != nil {
				return fmt.Errorf("修改密码失败：%w", translate(err))
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if grants != nil {
		if err := s.applyGrants(name, grants); err != nil {
			return err
		}
	}
	return nil
}

// DropRole 删除账号。
//
// 先把他拥有的对象转给管理账号再删：直接 DROP ROLE 会因为"角色拥有对象"失败，
// 而顺带 DROP OWNED 会把业务数据一起删掉——那是用户没想到的破坏。
func (s Spec) DropRole(name string) error {
	if err := mysqlctl.ValidRoleName(name); err != nil {
		return err
	}
	if systemRoles[strings.ToLower(name)] {
		return errors.New("系统账号不能删除")
	}
	dbs, err := s.listDatabaseNames()
	if err == nil {
		for _, dbName := range dbs {
			_ = s.withConn(dbName, func(ctx context.Context, db *sql.DB) error {
				_, _ = db.ExecContext(ctx, "REASSIGN OWNED BY "+quoteIdent(name)+" TO "+quoteIdent(SuperUser))
				return nil
			})
		}
	}
	return s.withConn(RootUser, func(ctx context.Context, db *sql.DB) error {
		if _, err := db.ExecContext(ctx, "DROP ROLE "+quoteIdent(name)); err != nil {
			return fmt.Errorf("删除用户失败：%w", translate(err))
		}
		return nil
	})
}

// applyGrants 把账号的库授权改成给定集合（以这份为准，含撤销）。
func (s Spec) applyGrants(role string, want []mysqlctl.RoleGrant) error {
	wanted := make(map[string]string, len(want))
	for _, g := range want {
		wanted[strings.ToLower(strings.TrimSpace(g.Database))] = g.Level
	}

	// 先撤销不在集合里的库（含把档位降下来的重授）
	for _, g := range s.grantsOf(role) {
		if _, keep := wanted[strings.ToLower(g.Database)]; keep {
			continue
		}
		action := revokeAll(role, g.Database)
		if err := s.withConn(g.Database, action); err != nil {
			return fmt.Errorf("撤销 %s 上的授权失败：%w", g.Database, err)
		}
	}

	for _, g := range want {
		dbName := strings.TrimSpace(g.Database)
		if dbName == "" {
			continue
		}
		if err := s.withConn(dbName, grantAction(role, dbName, g.Level)); err != nil {
			return fmt.Errorf("授权 %s 失败：%w", dbName, err)
		}
	}
	return nil
}

// revokeAll 收回角色在某个库上的全部授权。
func revokeAll(role, dbName string) func(ctx context.Context, db *sql.DB) error {
	return func(ctx context.Context, db *sql.DB) error {
		stmts := []string{
			"REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM " + quoteIdent(role),
			"REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM " + quoteIdent(role),
			"REVOKE ALL PRIVILEGES ON SCHEMA public FROM " + quoteIdent(role),
			"REVOKE ALL PRIVILEGES ON DATABASE " + quoteIdent(dbName) + " FROM " + quoteIdent(role),
			// 默认权限也要撤，否则以后新建的表又会自动带上旧权限
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL ON TABLES FROM " + quoteIdent(role),
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL ON SEQUENCES FROM " + quoteIdent(role),
		}
		for _, stmt := range stmts {
			// 本来就没有权限时 REVOKE 只是警告，出错也不该中断
			_, _ = db.ExecContext(ctx, stmt)
		}
		return nil
	}
}

// grantAction 按档位生成授权语句。
//
// 档位语义与 MySQL 一套完全一致，用户不必重新学：
//   - read  ：能连、能 SELECT
//   - write ：read + INSERT/UPDATE/DELETE
//   - all   ：write + 建表改表这类结构权限
//   - none  ：连不上（撤销 CONNECT）
func grantAction(role, dbName, level string) func(ctx context.Context, db *sql.DB) error {
	return func(ctx context.Context, db *sql.DB) error {
		// 先收再授：把 all 降到 read 时，建表权限必须真的消失
		if err := revokeAll(role, dbName)(ctx, db); err != nil {
			return err
		}
		if level == mysqlctl.LevelNone || level == "" {
			return nil
		}
		ident := quoteIdent(role)
		stmts := []string{
			"GRANT CONNECT ON DATABASE " + quoteIdent(dbName) + " TO " + ident,
			"GRANT USAGE ON SCHEMA public TO " + ident,
			"GRANT SELECT ON ALL TABLES IN SCHEMA public TO " + ident,
			"GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO " + ident,
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO " + ident,
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON SEQUENCES TO " + ident,
		}
		if level == mysqlctl.LevelWrite || level == mysqlctl.LevelAll {
			stmts = append(stmts,
				"GRANT INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "+ident,
				"GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO "+ident,
				"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT INSERT, UPDATE, DELETE ON TABLES TO "+ident,
				"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO "+ident,
			)
		}
		if level == mysqlctl.LevelAll {
			stmts = append(stmts,
				"GRANT CREATE ON SCHEMA public TO "+ident,
				"GRANT TRUNCATE, REFERENCES, TRIGGER ON ALL TABLES IN SCHEMA public TO "+ident,
				"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT TRUNCATE, REFERENCES, TRIGGER ON TABLES TO "+ident,
			)
		}
		for _, stmt := range stmts {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return translate(err)
			}
		}
		return nil
	}
}
