package mysqlctl

import (
	"strings"
	"testing"

	"niudbm/server/internal/images"
)

// TestCnfCompatibleWithEveryImage 是配置项的"全版本体检"。
//
// 每个可部署版本都会按自己的 Flavor/Series 生成一份默认 my.cnf，这里逐条确认：
//   - 写进去的每个参数在这个实现/版本上真的存在（applies）；
//   - 枚举值在该版本上真的可用（optionsFor）；
//   - 参与 entrypoint chown 的路径都是容器内绝对路径（相对路径会让它扫根）；
//   - 渲染出来的文本能被解析器读回，键值不丢。
//
// 之前踩过的坑都在这里兜住：8.4 写 default_authentication_plugin、MariaDB 写
// innodb_redo_log_capacity、slow_query_log_file 写成相对路径。
func TestCnfCompatibleWithEveryImage(t *testing.T) {
	for _, img := range images.List() {
		if img.Family != images.FamilyMySQL && img.Family != images.FamilyMariaDB {
			continue
		}
		img := img
		t.Run(img.Name, func(t *testing.T) {
			spec := Spec{Flavor: img.Family, Series: img.Tag}
			entries := spec.initialCnf()
			if len(entries) == 0 {
				t.Fatalf("%s 生成了空配置", img.Name)
			}

			for key, value := range entries {
				d, ok := settingByKey[key]
				if !ok {
					t.Fatalf("%s 写入了未登记的参数 %s", img.Name, key)
				}
				if !d.applies(img.Family, img.Tag) {
					t.Errorf("%s（%s %s）不该出现 %s", img.Name, img.Family, img.Tag, key)
				}
				if d.kind == "enum" {
					allowed := optionsFor(d, img.Family, img.Tag)
					if !containsString(allowed, value) {
						t.Errorf("%s 的 %s=%s 不在可用选项 %v 里", img.Name, key, value, allowed)
					}
				}
				if isCnfDirKey(key) && !strings.HasPrefix(value, "/") {
					t.Errorf("%s 的 %s=%s 必须是容器内绝对路径，否则容器启动时会遍历整个文件系统",
						img.Name, key, value)
				}
			}

			// 渲染 → 解析回来，确认键值没在渲染阶段丢失或变形
			text := renderCnf(entries)
			back := parseCnf(text)
			for key, value := range entries {
				if back[key] != value {
					t.Errorf("%s 的 %s 渲染后读回变成 %q（原值 %q）", img.Name, key, back[key], value)
				}
			}
		})
	}
}

func TestInitialCnfVersionDefaults(t *testing.T) {
	mariadb := (Spec{Flavor: "mariadb", Series: "11.4"}).initialCnf()
	if mariadb["key_buffer_size"] != "128M" ||
		mariadb["table_open_cache"] != "2000" ||
		mariadb["max_allowed_packet"] != "16M" {
		t.Fatalf("MariaDB 默认值不正确：%v", mariadb)
	}
	mysql84 := (Spec{Flavor: "mysql", Series: "8.4"}).initialCnf()
	if mysql84["key_buffer_size"] != "8M" ||
		mysql84["table_open_cache"] != "4000" ||
		mysql84["max_allowed_packet"] != "64M" {
		t.Fatalf("MySQL 8.4 默认值不正确：%v", mysql84)
	}
	mysql57 := (Spec{Flavor: "mysql", Series: "5.7"}).initialCnf()
	if mysql57["character_set_server"] != "utf8mb4" || mysql57["collation_server"] != "utf8mb4_general_ci" {
		t.Fatalf("MySQL 5.7 字符集推荐值不正确：%v", mysql57)
	}
	if _, ok := mysql84["lower_case_table_names"]; ok {
		t.Fatal("lower_case_table_names 只能在初始化前设置，不应出现在可编辑模板")
	}
}

// TestCollationOptionsByEngine 守住排序规则的可选范围：
// utf8mb4_0900_ai_ci 只存在于 MySQL 8.0+，5.7 与 MariaDB 都没有。
func TestCollationOptionsByEngine(t *testing.T) {
	d := settingByKey["collation_server"]

	cases := []struct {
		flavor, series string
		want           bool
	}{
		{"mysql", "8.4", true},
		{"mysql", "9.7", true},
		{"mysql", "26.10", true},
		{"mysql", "8.0", true},
		{"mysql", "5.7", false},
		{"mariadb", "10.11", false},
		{"mariadb", "11.4", false},
		{"mariadb", "11.8", false},
	}
	for _, c := range cases {
		has := containsString(optionsFor(d, c.flavor, c.series), "utf8mb4_0900_ai_ci")
		if has != c.want {
			t.Errorf("%s %s 上 utf8mb4_0900_ai_ci 可用性应为 %v，实际 %v", c.flavor, c.series, c.want, has)
		}
	}
}

// TestValidateRejectsUnknownCollation 确认保存时会被挡住，而不是等容器起不来。
func TestValidateRejectsUnknownCollation(t *testing.T) {
	d := settingByKey["collation_server"]

	if _, err := validateSetting(d, "mariadb", "11.4", "utf8mb4_0900_ai_ci"); err == nil {
		t.Fatal("MariaDB 上不该接受 utf8mb4_0900_ai_ci")
	}
	if _, err := validateSetting(d, "mysql", "8.4", "utf8mb4_0900_ai_ci"); err != nil {
		t.Fatalf("MySQL 8.4 应接受 utf8mb4_0900_ai_ci：%v", err)
	}
}

// TestCharsetPairChecksEngine 确认"前缀对但版本没有"的组合也会被拒绝。
func TestCharsetPairChecksEngine(t *testing.T) {
	spec := Spec{Flavor: "mariadb", Series: "11.4"}
	err := spec.checkCharsetPair(map[string]string{
		"character_set_server": "utf8mb4",
		"collation_server":     "utf8mb4_0900_ai_ci",
	})
	if err == nil {
		t.Fatal("MariaDB 上 utf8mb4_0900_ai_ci 应被拒绝")
	}

	ok := Spec{Flavor: "mysql", Series: "8.4"}
	if err := ok.checkCharsetPair(map[string]string{
		"character_set_server": "utf8mb4",
		"collation_server":     "utf8mb4_0900_ai_ci",
	}); err != nil {
		t.Fatalf("MySQL 8.4 上应通过：%v", err)
	}

	if err := ok.checkCharsetPair(map[string]string{
		"character_set_server": "utf8mb4",
		"collation_server":     "latin1_swedish_ci",
	}); err == nil {
		t.Fatal("字符集与排序规则不匹配应被拒绝")
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
