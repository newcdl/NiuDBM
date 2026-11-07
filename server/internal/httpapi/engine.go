package httpapi

import (
	"errors"
	"io"
	"time"

	"niudbm/server/internal/images"
	"niudbm/server/internal/mongoctl"
	"niudbm/server/internal/mysqlctl"
	"niudbm/server/internal/pgctl"
	"niudbm/server/internal/store"
)

// 支持的引擎标识。台账 instances.engine 与镜像清单的 family 都用这两个值。
const (
	engineMySQL    = "mysql"
	enginePostgres = "postgres"
	engineMongoDB  = "mongodb"

	instanceModeManaged  = "managed"
	instanceModeExternal = "external"
)

func isExternal(in store.Instance) bool {
	return in.Mode == instanceModeExternal
}

// dbRuntime 是一个实例的管理能力。
//
// 两个引擎在这层的语义是刻意对齐的：库、账号档位（read/write/all/none）、
// 参数白名单、按库备份——用户换引擎不需要重新学一套界面。
// 差异都收在 mysqlctl / pgctl 里，handler 只认这个接口。
type dbRuntime interface {
	Running() bool
	Start() error
	Stop() error
	WaitReady(time.Duration) error
	Provision(password, seedSQL string) error
	CountTables() (int, error)

	ListDatabases() ([]mysqlctl.Database, error)
	CreateDatabase(name, charset string) error
	DropDatabase(name string) error
	Charsets() ([]mysqlctl.Charset, error)

	Tables(dbName string) ([]string, error)
	ExportCSV(out io.Writer, dbName, table string, limit int) (int64, error)
	ImportCSV(dbName, table string, r io.Reader) (int64, error)
	ExecScript(dbName, script string) (int, error)

	Roles() ([]mysqlctl.Role, error)
	CreateRole(name string, hosts []string, password string, grants []mysqlctl.RoleGrant) error
	CreateRoleOn(dbName, name string, hosts []string, password, level string) error
	UpdateRole(name string, hosts []string, password string, grants []mysqlctl.RoleGrant) error
	DropRole(name string) error

	Settings() ([]mysqlctl.Setting, error)
	ApplySettings(values map[string]string) (bool, error)
	Status() ([]mysqlctl.StatusMetric, error)

	Backup(dst, database string) error
	Restore(src string) error
	VerifyBackup(path string) error
}

// dbSpec 按台账里的引擎还原成对应的操作对象。
func (s *Server) dbSpec(in store.Instance) (dbRuntime, error) {
	switch in.Engine {
	case engineMongoDB:
		if isExternal(in) {
			return mongoctl.Spec{
				Series: in.EngineVersion, Flavor: images.FamilyMongo,
				Host: in.Host, User: in.Username, External: true, TLSMode: in.TLSMode,
				Port: in.Port, Password: s.readExternalSecret(in.ID),
			}, nil
		}
		return s.mongoSpec(in)
	case enginePostgres:
		if isExternal(in) {
			return pgctl.Spec{
				Series:   in.EngineVersion,
				Flavor:   images.FamilyPostgres,
				Host:     in.Host,
				User:     in.Username,
				External: true,
				TLSMode:  in.TLSMode,
				Port:     in.Port,
				Password: s.readExternalSecret(in.ID),
			}, nil
		}
		return s.pgSpec(in)
	case engineMySQL, "":
		if isExternal(in) {
			return mysqlctl.Spec{
				Series:   in.EngineVersion,
				Flavor:   images.FamilyMySQL,
				Host:     in.Host,
				User:     in.Username,
				External: true,
				TLSMode:  in.TLSMode,
				Port:     in.Port,
				Password: s.readExternalSecret(in.ID),
			}, nil
		}
		// 空值只可能是升级上来的老记录，仍按 MySQL 处理
		return s.mysqlSpec(in)
	}
	return nil, errors.New("不支持的引擎：" + in.Engine)
}

// pgSpec 把台账记录还原成 PostgreSQL 容器操作需要的参数。
func (s *Server) pgSpec(in store.Instance) (pgctl.Spec, error) {
	img, ok := images.FindFamily(images.FamilyPostgres, in.EngineVersion)
	if !ok || img.Family != images.FamilyPostgres {
		return pgctl.Spec{}, errors.New("找不到版本 " + in.EngineVersion + " 的镜像信息")
	}
	return pgctl.Spec{
		Series: img.Tag,
		Flavor: img.Family,
		// 只有绑挂方式才有宿主目录；命名卷的体积由 docker 管，界面按"未知"显示
		DataDir:  s.storageDir(in),
		Port:     in.Port,
		Password: s.readSecret(in.ID),
		Docker: &pgctl.DockerRuntime{
			Client:       s.dockerClient(),
			Container:    containerNameOf(in),
			RootPassword: s.readRootSecret(in.ID),
		},
	}, nil
}

func (s *Server) mongoSpec(in store.Instance) (mongoctl.Spec, error) {
	img, ok := images.FindFamily(images.FamilyMongo, in.EngineVersion)
	if !ok || img.Family != images.FamilyMongo {
		return mongoctl.Spec{}, errors.New("找不到 MongoDB 版本 " + in.EngineVersion)
	}
	return mongoctl.Spec{
		Series: img.Tag, Flavor: img.Family, Port: in.Port,
		Password: s.readRootSecret(in.ID),
		Docker: &mongoctl.DockerRuntime{
			Client: s.dockerClient(), Container: containerNameOf(in),
			RootPassword: s.readRootSecret(in.ID),
		},
	}, nil
}

// validDBName 按实例所属引擎校验库名。
//
// 两个引擎的合法字符集不完全一样（MySQL 只认字母数字下划线，PG 还允许短横线与大写），
// 所以不能统一用 MySQL 那条规则——那会把 PG 能建的库名挡在门外。
func validDBName(in store.Instance, name string) error {
	if in.Engine == engineMongoDB {
		return mongoctl.ValidDBName(name)
	}
	if in.Engine == enginePostgres {
		return pgctl.ValidDBName(name)
	}
	return mysqlctl.ValidDBName(name)
}

func validRoleNameFor(in store.Instance, name string) error {
	if in.Engine == engineMongoDB {
		return mongoctl.ValidRoleName(name)
	}
	return mysqlctl.ValidRoleName(name)
}

// engineLabel 返回给用户看的引擎名。
func engineLabel(engine string) string {
	switch engine {
	case enginePostgres:
		return pgctl.Label
	case engineMongoDB:
		return mongoctl.Label
	case engineMySQL, "":
		return mysqlctl.Label
	}
	return engine
}
