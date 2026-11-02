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

// DefaultImage 返回默认版本；清单为空时给一个安全值。
func DefaultImage() Image {
	for _, img := range catalog {
		if img.Default {
			return img
		}
	}
	if len(catalog) > 0 {
		return catalog[0]
	}
	return Image{Tag: "8.4", Name: "mysql:8.4", Family: FamilyMySQL, Label: "MySQL 8.4"}
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
