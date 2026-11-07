package httpapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"niudbm/server/internal/images"
	"niudbm/server/internal/platform"
	"niudbm/server/internal/store"
)

// TestDockerPlanMySQLVersionArgs 守住 MySQL 各版本的容器启动差异：
// 任何版本都不追加 mysqld 参数。--mysqlx 看起来只是关掉容器内的 33060，
// 但它会让 entrypoint 的 `mysqld --verbose --help` 解析失败，进而把
// datadir/socket 变成空串、触发对容器根目录的 chown。
func TestDockerPlanMySQLVersionArgs(t *testing.T) {
	s := New(platform.Env{PkgVar: t.TempDir()}, "", nil, Paths{Instances: t.TempDir()})

	cases := []struct {
		tag   string
		image string
		wantX bool
	}{
		{tag: "8.4", image: "mysql:8.4", wantX: false},
		{tag: "8.0", image: "mysql:8.0", wantX: false},
		{tag: "5.7", image: "mysql:5.7", wantX: false},
	}
	for _, c := range cases {
		img, ok := images.Find(c.tag)
		if !ok {
			t.Fatalf("清单里没有 MySQL %s", c.tag)
		}
		if img.Name != c.image {
			t.Fatalf("MySQL %s 的镜像应为 %s，实际 %s", c.tag, c.image, img.Name)
		}
		in := store.Instance{
			ID: "01M5Y181Z2N2H2S1RPQ2S0TK08", Engine: images.FamilyMySQL,
			EngineVersion: c.tag, Runtime: runtimeDocker, Port: 33000,
		}
		plan, err := s.planFor(in, img, "secret", "secret")
		if err != nil {
			t.Fatalf("MySQL %s 生成容器参数失败：%v", c.tag, err)
		}
		got := strings.Join(plan.Command, " ")
		if strings.Contains(got, "--mysqlx") != c.wantX {
			t.Fatalf("MySQL %s 的启动参数不该是 %q", c.tag, got)
		}
		if plan.Env["MYSQL_ROOT_PASSWORD"] != "secret" {
			t.Fatalf("MySQL %s 应设置 MYSQL_ROOT_PASSWORD：%v", c.tag, plan.Env)
		}
		if plan.ContainerPort != containerMySQLPort || plan.DataPath != containerDataPath {
			t.Fatalf("MySQL %s 的端口/数据目录不对：%+v", c.tag, plan)
		}
	}
}

// TestDockerPlanPostgres 守住 PostgreSQL 的容器参数：
// 端口 5432、数据目录 /var/lib/postgresql/data、只设 POSTGRES_PASSWORD，
// 且不挂 MySQL 的 conf.d（PG 的参数走 ALTER SYSTEM）。
func TestDockerPlanPostgres(t *testing.T) {
	s := New(platform.Env{PkgVar: t.TempDir()}, "", nil, Paths{Instances: t.TempDir()})
	img, ok := images.Find("17")
	if !ok || img.Family != images.FamilyPostgres {
		t.Fatal("清单里没有 PostgreSQL 17")
	}
	in := store.Instance{
		ID: "01M5Y181Z2N2H2S1RPQ2S0TK09", Engine: enginePostgres,
		EngineVersion: "17", Runtime: runtimeDocker, Port: 33001,
		DataDir: "01M5Y181Z2N2H2S1RPQ2S0TK09/postgres",
	}
	plan, err := s.planFor(in, img, "root-secret", "app-secret")
	if err != nil {
		t.Fatal(err)
	}
	if plan.ContainerPort != containerPostgresPort {
		t.Fatalf("PG 容器端口应为 %d，得到 %d", containerPostgresPort, plan.ContainerPort)
	}
	if plan.DataPath != containerPgDataPath {
		t.Fatalf("PG 数据目录不对：%s", plan.DataPath)
	}
	if plan.ConfDir != "" {
		t.Fatalf("PG 不应挂 conf.d：%s", plan.ConfDir)
	}
	if plan.Env["POSTGRES_PASSWORD"] != "root-secret" {
		t.Fatalf("应设置 POSTGRES_PASSWORD：%v", plan.Env)
	}
	if plan.Env["MYSQL_ROOT_PASSWORD"] != "" || plan.Env["MARIADB_ROOT_PASSWORD"] != "" {
		t.Fatalf("PG 不该带 MySQL 系变量：%v", plan.Env)
	}
	// 宿主端口来自台账，容器内固定 5432；两者混用会让容器起来了却连不上
	if plan.ContainerPort == containerMySQLPort {
		t.Fatal("PG 不应映射到 3306")
	}
}

func TestDockerPlanMongoDB(t *testing.T) {
	s := New(platform.Env{PkgVar: t.TempDir()}, "", nil, Paths{Instances: t.TempDir()})
	in := store.Instance{
		ID: "01MONGO0000000000000000000", Name: "mongo", Engine: engineMongoDB,
		EngineVersion: "8.0", Port: 27017, Runtime: runtimeDocker,
		StorageKind: storageVolume, DataDir: "01MONGO0000000000000000000/mongodb",
	}
	img, ok := images.FindFamily(images.FamilyMongo, "8.0")
	if !ok {
		t.Fatal("缺少 MongoDB 8.0 镜像")
	}
	plan, err := s.planFor(in, img, "RootPass_42", "AppPass_42")
	if err != nil {
		t.Fatal(err)
	}
	if plan.ContainerPort != containerMongoPort || plan.DataPath != containerMongoPath {
		t.Fatalf("MongoDB 容器参数不对：%+v", plan)
	}
	if plan.Env["MONGO_INITDB_ROOT_USERNAME"] != "root" || plan.Env["MONGO_INITDB_ROOT_PASSWORD"] != "RootPass_42" {
		t.Fatalf("MongoDB 初始化环境变量不对：%+v", plan.Env)
	}
	if plan.ConfTarget != "/etc/mongodb" || len(plan.Command) != 3 || plan.Command[2] != "/etc/mongodb/mongod.conf" {
		t.Fatalf("MongoDB 配置文件挂载或启动命令不正确：%+v", plan)
	}
	if _, err := os.Stat(filepath.Join(plan.ConfDir, "mongod.conf")); err != nil {
		t.Fatalf("MongoDB 默认配置文件未生成：%v", err)
	}
}

// TestPickVersionCrossFamily 挡住"把 MySQL 版本号填给 PG"这种错配。
func TestPickVersionCrossFamily(t *testing.T) {
	s := New(platform.Env{PkgVar: t.TempDir()}, "", nil, Paths{Instances: t.TempDir()})

	img, err := s.pickVersionFor(enginePostgres, "")
	if err != nil || img.Family != images.FamilyPostgres {
		t.Fatalf("PG 空版本应落到 PG 默认版本：%v / %+v", err, img)
	}
	if _, err := s.pickVersionFor(enginePostgres, "8.4"); err == nil {
		t.Fatal("MySQL 的版本号不能用在 PG 上")
	}
	if _, err := s.pickVersionFor(engineMySQL, "17"); err == nil {
		t.Fatal("PG 的版本号不能用在 MySQL 上")
	}
	if img, err := s.pickVersionFor(engineMySQL, ""); err != nil || img.Family != images.FamilyMySQL {
		t.Fatalf("MySQL 空版本应仍是 MySQL 默认：%v / %+v", err, img)
	}
}

// TestImageCandidates 守住"加速站优先、官方源兜底"的顺序：
// 少了兜底，镜像站一挂整个部署流程就不可用。
func TestImageCandidates(t *testing.T) {
	withMirror := New(platform.Env{PkgVar: t.TempDir(), DockerMirror: "docker.m.daocloud.io/"}, "", nil, Paths{Instances: t.TempDir()})
	got := withMirror.imageCandidates("mysql:8.4")
	want := []string{"docker.m.daocloud.io/library/mysql:8.4", "mysql:8.4"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("加速站候选顺序不对：%v", got)
	}

	plain := New(platform.Env{PkgVar: t.TempDir()}, "", nil, Paths{Instances: t.TempDir()})
	if got := plain.imageCandidates("mysql:8.4"); len(got) != 1 || got[0] != "mysql:8.4" {
		t.Fatalf("没配加速站时只应尝试官方名：%v", got)
	}
}

// TestDockerPlanMariaDBEnv 确认 MariaDB 仍在用 MARIADB_* 变量，别被 MySQL 的改动带偏。
func TestDockerPlanMariaDBEnv(t *testing.T) {
	s := New(platform.Env{PkgVar: t.TempDir()}, "", nil, Paths{Instances: t.TempDir()})
	img, ok := images.Find("11.4")
	if !ok {
		t.Fatal("清单里没有 MariaDB 11.4")
	}
	in := store.Instance{
		ID: "01M5Y181Z2N2H2S1RPQ2S0TK08", Engine: images.FamilyMariaDB,
		EngineVersion: "11.4", Runtime: runtimeDocker, Port: 33000,
	}
	plan, err := s.planFor(in, img, "secret", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Env["MARIADB_ROOT_PASSWORD"] != "secret" || plan.Env["MYSQL_ROOT_PASSWORD"] != "" {
		t.Fatalf("MariaDB 应使用 MARIADB_* 变量：%v", plan.Env)
	}
	if strings.Contains(strings.Join(plan.Command, " "), "--mysqlx") {
		t.Fatalf("MariaDB 没有 mysqlx 参数：%v", plan.Command)
	}
}

// TestPickVersion 守住版本解析：空值用默认 LTS，未知 tag 与不支持当前架构都要明确拒绝。
func TestPickVersion(t *testing.T) {
	s := New(platform.Env{PkgVar: t.TempDir()}, "", nil, Paths{Instances: t.TempDir()})

	img, err := s.pickVersionFor(engineMySQL, "")
	if err != nil {
		t.Fatalf("空版本应回落到默认：%v", err)
	}
	want, _ := images.DefaultFor(images.FamilyMySQL)
	if img.Name != want.Name {
		t.Fatalf("默认版本不对：%s", img.Name)
	}

	// 完整镜像名也接受：老数据与用户直接输入都能命中
	if img, err := s.pickVersionFor(engineMySQL, "mysql:8.0"); err != nil || img.Tag != "8.0" {
		t.Fatalf("完整镜像名应能解析：%v / %+v", err, img)
	}

	if _, err := s.pickVersionFor(engineMySQL, "9.9"); err == nil {
		t.Fatal("不存在的版本应报错")
	}
}
