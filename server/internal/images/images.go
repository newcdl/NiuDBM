// Package images 是唯一的"版本清单"：一个 tag 就是一个可部署的数据库版本。
//
// 引擎不再随包分发，也不再落地到宿主：所有版本都是官方镜像的 tag。
// 这份清单只做两件事——给界面列可选项、给部署校验 tag 是否存在与架构是否支持。
package images

import "strings"

// Image 是一个可部署的镜像版本。
type Image struct {
	// Tag 是台账里记录的版本（例如 8.4 / 11.4），界面用它选择版本。
	Tag string
	// Name 是完整镜像名（例如 mysql:8.4），docker run 与 docker pull 都用它。
	Name string
	// Family 决定初始化参数与客户端命令：mysql 用 mysqldump/mysql，mariadb 用 mariadb-dump/mariadb。
	Family string
	Label  string
	// Archs 是官方提供该镜像的架构；不在列表里的架构直接标不可用，省得用户等一次失败的拉取。
	Archs []string
	// Default 标记默认版本（只有最新的 LTS 才是）。
	Default bool
	// Note 是界面上的一句提示（例如"仅 x86_64"）。
	Note string
}

const (
	FamilyMySQL   = "mysql"
	FamilyMariaDB = "mariadb"
	// FamilyPostgres 是 PostgreSQL 官方镜像的 family。
	// 与 MySQL 系的差别在初始化变量（POSTGRES_PASSWORD）、客户端工具（psql/pg_dump）
	// 和权限模型（库级 CONNECT + schema 级授权），相关分支集中在 pgctl。
	FamilyPostgres = "postgres"
	// FamilyMongo 使用官方 MongoDB 镜像与 mongodump/mongorestore。
	FamilyMongo = "mongodb"
)

// 版本来源：MySQL 官方 2026-10 发布的 26.10.0 / 9.7.4 / 8.4.13 与 8.0 维护线。
// 架构按 `docker manifest inspect` 实测填写：写错了会让用户白等一次注定失败的拉取。
var catalog = []Image{
	{
		Tag: "8.4", Name: "mysql:8.4", Family: FamilyMySQL, Label: "MySQL 8.4 LTS", Default: true,
		Archs: []string{"amd64", "arm64"},
		Note:  "长期支持版，生产优先选它；tag 浮动，拉取时拿当前最新小版本（8.4.13）",
	},
	{
		Tag: "8.4.13", Name: "mysql:8.4.13", Family: FamilyMySQL, Label: "MySQL 8.4.13",
		Archs: []string{"amd64", "arm64"},
		Note:  "8.4 LTS 的固定小版本，不想被浮动 tag 带着升级就选它",
	},
	{
		Tag: "9.7", Name: "mysql:9.7", Family: FamilyMySQL, Label: "MySQL 9.7",
		Archs: []string{"amd64", "arm64"},
		Note:  "创新版本，特性新但不做长期支持",
	},
	{
		Tag: "26.10", Name: "mysql:26.10", Family: FamilyMySQL, Label: "MySQL 26.10（最新）",
		Archs: []string{"amd64", "arm64"},
		Note:  "2026 年起的版本号方案，尝鲜可以，生产仍建议 8.4 LTS",
	},
	{
		Tag: "8.0", Name: "mysql:8.0", Family: FamilyMySQL, Label: "MySQL 8.0",
		Archs: []string{"amd64", "arm64"},
		Note:  "老项目兼容用，8.0 已进入维护尾声，新项目建议 8.4",
	},
	{
		Tag: "5.7", Name: "mysql:5.7", Family: FamilyMySQL, Label: "MySQL 5.7（老项目兼容）",
		Archs: []string{"amd64"}, Note: "官方只发布 x86_64 镜像，ARM 机器上装不了",
	},
	{
		Tag: "11.8", Name: "mariadb:11.8", Family: FamilyMariaDB, Label: "MariaDB 11.8 LTS",
		Archs: []string{"amd64", "arm64"},
	},
	{
		Tag: "11.4", Name: "mariadb:11.4", Family: FamilyMariaDB, Label: "MariaDB 11.4 LTS",
		Archs: []string{"amd64", "arm64"},
	},
	{
		Tag: "10.11", Name: "mariadb:10.11", Family: FamilyMariaDB, Label: "MariaDB 10.11 LTS",
		Archs: []string{"amd64", "arm64"},
	},
	{
		Tag: "17", Name: "postgres:17", Family: FamilyPostgres, Label: "PostgreSQL 17", Default: true,
		Archs: []string{"amd64", "arm64"},
		Note:  "当前稳定大版本，生产优先选它",
	},
	{
		Tag: "16", Name: "postgres:16", Family: FamilyPostgres, Label: "PostgreSQL 16",
		Archs: []string{"amd64", "arm64"},
		Note:  "上一代稳定版本，扩展生态兼容性最好",
	},
	{
		Tag: "15", Name: "postgres:15", Family: FamilyPostgres, Label: "PostgreSQL 15（老项目兼容）",
		Archs: []string{"amd64", "arm64"},
		Note:  "老项目仍在用；新项目建议 16 或 17",
	},
	{
		Tag: "8.0", Name: "mongo:8.0", Family: FamilyMongo, Label: "MongoDB 8.0", Default: true,
		Archs: []string{"amd64", "arm64"},
		Note:  "当前稳定主版本，适合新部署",
	},
	{
		Tag: "7.0", Name: "mongo:7.0", Family: FamilyMongo, Label: "MongoDB 7.0",
		Archs: []string{"amd64", "arm64"},
		Note:  "兼容上一代 MongoDB 工具链",
	},
	{
		Tag: "6.0", Name: "mongo:6.0", Family: FamilyMongo, Label: "MongoDB 6.0（老项目兼容）",
		Archs: []string{"amd64", "arm64"},
	},
}

// List 返回全部可部署版本（顺序即界面顺序）。
func List() []Image {
	out := make([]Image, len(catalog))
	copy(out, catalog)
	return out
}

// Find 按 tag 查版本；tag 也接受完整镜像名（mysql:8.4），方便老数据与直接输入。
func Find(tag string) (Image, bool) {
	tag = strings.TrimSpace(tag)
	for _, img := range catalog {
		if img.Tag == tag || img.Name == tag {
			return img, true
		}
	}
	return Image{}, false
}

// FindFamily 在指定 family 内按 tag 或完整镜像名查版本。
// MySQL 8.0 与 MongoDB 8.0 这类 tag 会跨引擎重复，部署与重建必须带 family 判断。
func FindFamily(family, tag string) (Image, bool) {
	tag = strings.TrimSpace(tag)
	for _, img := range catalog {
		if img.Family == family && (img.Tag == tag || img.Name == tag) {
			return img, true
		}
	}
	return Image{}, false
}

// ListFamily 返回某个 family 的全部版本，顺序即界面顺序。
func ListFamily(family string) []Image {
	out := make([]Image, 0, len(catalog))
	for _, img := range catalog {
		if img.Family == family {
			out = append(out, img)
		}
	}
	return out
}

// DefaultFor 返回某个 family 的默认版本。
func DefaultFor(family string) (Image, bool) {
	var first Image
	found := false
	for _, img := range catalog {
		if img.Family != family {
			continue
		}
		if !found {
			first, found = img, true
		}
		if img.Default {
			return img, true
		}
	}
	return first, found
}

// Supports 判断当前架构能不能跑这个镜像。
func (i Image) Supports(arch string) bool {
	arch = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(arch)), "linux/")
	if arch == "" {
		return true
	}
	for _, a := range i.Archs {
		if a == arch {
			return true
		}
	}
	return false
}
