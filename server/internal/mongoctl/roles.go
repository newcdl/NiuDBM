package mongoctl

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"niudbm/server/internal/mysqlctl"
)

var roleNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)

func ValidRoleName(name string) error {
	name = strings.TrimSpace(name)
	if !roleNamePattern.MatchString(name) {
		return errors.New("MongoDB 用户名只能包含字母、数字、下划线、点和短横线，且以字母或下划线开头")
	}
	if strings.EqualFold(name, RootUser) {
		return errors.New("root 是保留用户名")
	}
	return nil
}

func ValidLevel(level string) bool {
	switch level {
	case mysqlctl.LevelNone, mysqlctl.LevelRead, mysqlctl.LevelWrite, mysqlctl.LevelAll:
		return true
	}
	return false
}

func Levels() []string {
	return []string{mysqlctl.LevelNone, mysqlctl.LevelRead, mysqlctl.LevelWrite, mysqlctl.LevelAll}
}

type roleInfo struct {
	Role string `bson:"role"`
	DB   string `bson:"db"`
}

func (s Spec) Roles() ([]mysqlctl.Role, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := s.client(ctx)
	if err != nil {
		return nil, translate(err)
	}
	defer client.Disconnect(context.Background())

	var result struct {
		Users []struct {
			User  string     `bson:"user"`
			DB    string     `bson:"db"`
			Roles []roleInfo `bson:"roles"`
		} `bson:"users"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "usersInfo", Value: 1}}).Decode(&result); err != nil {
		return nil, translate(err)
	}
	out := make([]mysqlctl.Role, 0, len(result.Users))
	for _, u := range result.Users {
		if strings.EqualFold(u.User, RootUser) {
			continue
		}
		grants := make([]mysqlctl.RoleGrant, 0, len(u.Roles))
		for _, role := range u.Roles {
			if role.DB == "" || role.DB == "admin" {
				continue
			}
			level := mysqlctl.LevelRead
			switch role.Role {
			case "readWrite":
				level = mysqlctl.LevelWrite
			case "dbOwner", "dbAdmin", "readWriteAnyDatabase", "dbAdminAnyDatabase", "root":
				level = mysqlctl.LevelAll
			case "read":
				level = mysqlctl.LevelRead
			default:
				continue
			}
			grants = append(grants, mysqlctl.RoleGrant{Database: role.DB, Level: level})
		}
		level := mysqlctl.LevelNone
		if len(grants) > 0 {
			level = grants[0].Level
		}
		out = append(out, mysqlctl.Role{
			Name: u.User, Login: true, Level: level, Grants: grants, Hosts: nil,
		})
	}
	return out, nil
}

func mongoRoles(grants []mysqlctl.RoleGrant) bson.A {
	roles := bson.A{}
	for _, grant := range grants {
		if grant.Level == mysqlctl.LevelNone || grant.Database == "" {
			continue
		}
		role := "read"
		switch grant.Level {
		case mysqlctl.LevelWrite:
			role = "readWrite"
		case mysqlctl.LevelAll:
			role = "dbOwner"
		}
		roles = append(roles, bson.D{{Key: "role", Value: role}, {Key: "db", Value: grant.Database}})
	}
	return roles
}

func (s Spec) CreateRole(name string, _ []string, password string, grants []mysqlctl.RoleGrant) error {
	if err := ValidRoleName(name); err != nil {
		return err
	}
	if len(password) < 8 {
		return errors.New("MongoDB 密码至少 8 位")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := s.client(ctx)
	if err != nil {
		return translate(err)
	}
	defer client.Disconnect(context.Background())
	cmd := bson.D{
		{Key: "createUser", Value: name},
		{Key: "pwd", Value: password},
		{Key: "roles", Value: mongoRoles(grants)},
	}
	return translate(client.Database("admin").RunCommand(ctx, cmd).Err())
}

func (s Spec) CreateRoleOn(dbName, name string, hosts []string, password, level string) error {
	return s.CreateRole(name, hosts, password, []mysqlctl.RoleGrant{{Database: dbName, Level: level}})
}

// UpdateRole 更新角色；MongoDB 没有 host 来源概念，hosts 参数会被忽略。
func (s Spec) UpdateRole(name string, _ []string, password string, grants []mysqlctl.RoleGrant) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := s.client(ctx)
	if err != nil {
		return translate(err)
	}
	defer client.Disconnect(context.Background())
	cmd := bson.D{
		{Key: "updateUser", Value: name},
		{Key: "roles", Value: mongoRoles(grants)},
	}
	if password != "" {
		cmd = append(cmd, bson.E{Key: "pwd", Value: password})
	}
	return translate(client.Database("admin").RunCommand(ctx, cmd).Err())
}

func (s Spec) DropRole(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := s.client(ctx)
	if err != nil {
		return translate(err)
	}
	defer client.Disconnect(context.Background())
	return translate(client.Database("admin").RunCommand(ctx, bson.D{
		{Key: "dropUser", Value: name},
	}).Err())
}

func (s Spec) Settings() ([]mysqlctl.Setting, error) {
	return []mysqlctl.Setting{}, nil
}

func (s Spec) ApplySettings(map[string]string) (bool, error) {
	return false, errors.New("MongoDB 暂不提供在线参数编辑")
}
