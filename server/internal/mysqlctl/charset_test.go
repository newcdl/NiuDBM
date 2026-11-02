package mysqlctl

import "testing"

// 排序规则要挑"跨版本稳定"的那个：各版本的默认值不一样，
// 同一套建库参数不该因为换了个镜像就换了排序行为。
func TestPickCollation(t *testing.T) {
	cases := []struct {
		name string
		list []collationRow
		want string
	}{
		{
			name: "优先 general_ci，即使默认是 0900",
			list: []collationRow{
				{name: "utf8mb4_0900_ai_ci", isDefault: true},
				{name: "utf8mb4_general_ci"},
				{name: "utf8mb4_bin"},
			},
			want: "utf8mb4_general_ci",
		},
		{
			name: "没有 general_ci 时退回默认值",
			list: []collationRow{
				{name: "gb18030_chinese_ci", isDefault: true},
				{name: "gb18030_bin"},
			},
			want: "gb18030_chinese_ci",
		},
		{
			name: "binary 字符集自己就是 collation",
			list: []collationRow{{name: "binary", isDefault: true}},
			want: "binary",
		},
		{
			name: "只剩 _bin 时用它",
			list: []collationRow{{name: "ucs2_bin"}},
			want: "ucs2_bin",
		},
		{name: "空列表", list: nil, want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickCollation(c.list); got != c.want {
				t.Fatalf("pickCollation() = %q，期望 %q", got, c.want)
			}
		})
	}
}

// 常用字符集必须排在下拉前面，否则找 utf8mb4 要翻很久。
func TestCharsetRankOrdersCommonFirst(t *testing.T) {
	if charsetRank("utf8mb4") >= charsetRank("cp1251") {
		t.Fatal("utf8mb4 应排在冷门字符集前面")
	}
	if charsetRank("gbk") >= charsetRank("ucs2") {
		t.Fatal("gbk 应排在 ucs2 前面")
	}
	if charsetRank("utf8mb3") >= charsetRank("latin2") {
		t.Fatal("utf8 系应排在 latin2 前面")
	}
}
