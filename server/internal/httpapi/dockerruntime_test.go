package httpapi

import (
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

	img, err := s.pickVersion("")
	if err != nil {
		t.Fatalf("空版本应回落到默认：%v", err)
	}
	if img.Name != images.DefaultImage().Name {
		t.Fatalf("默认版本不对：%s", img.Name)
	}

	// 完整镜像名也接受：老数据与用户直接输入都能命中
	if img, err := s.pickVersion("mysql:8.0"); err != nil || img.Tag != "8.0" {
		t.Fatalf("完整镜像名应能解析：%v / %+v", err, img)
	}

	if _, err := s.pickVersion("9.9"); err == nil {
		t.Fatal("不存在的版本应报错")
	}
}
