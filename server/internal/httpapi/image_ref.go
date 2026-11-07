package httpapi

import (
	"errors"
	"strings"
)

// 自定义镜像地址。
//
// 为什么要让用户自己填：官方地址（docker.io）在国内很多网络下根本拉不动，
// 而"能用哪个加速站"因网络而异——写死一个等于把另一部分用户挡在外面。
// 但也不能什么都不校验：地址里混进空格或换行，最后会变成 docker 的一个
// 莫名其妙的参数错误，用户看不出是自己填错了。
//
// 校验刻意保持宽松（真正的权威是 docker pull），只挡住明显不成立的写法：
//   - 空、含空白/控制字符
//   - 没有版本标签（:latest 太危险，等同"哪天悄悄换了版本"）
//   - 含除 . _ - / : @ 之外的字符
const maxImageRefLen = 200

// normalizeImageRef 校验并规整一个镜像地址。
func normalizeImageRef(raw string) (string, error) {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return "", errors.New("镜像地址不能为空")
	}
	if len(ref) > maxImageRefLen {
		return "", errors.New("镜像地址过长")
	}
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == '/' || r == ':' || r == '@':
		default:
			return "", errors.New("镜像地址只能包含字母、数字与 . _ - / : @")
		}
	}
	if strings.Contains(ref, "//") || strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") {
		return "", errors.New("镜像地址格式不对，例如：docker.m.daocloud.io/library/postgres:17")
	}
	if imageTag(ref) == "" {
		return "", errors.New("镜像地址要带版本标签，例如 docker.m.daocloud.io/library/postgres:17")
	}
	return ref, nil
}

// imageTag 取出镜像地址里的标签（末段冒号后的部分，忽略仓库端口）。
//
// 例：registry:5000/db:17 → 17；postgres → ""；postgres:17 → 17；
// ghcr.io/a/b@sha256:xx → ""（摘要不是版本，不做猜测）
func imageTag(ref string) string {
	if strings.Contains(ref, "@") {
		return ""
	}
	slash := strings.LastIndexByte(ref, '/')
	colon := strings.LastIndexByte(ref, ':')
	if colon < 0 || colon < slash {
		return ""
	}
	return ref[colon+1:]
}

// imageOrDefault 优先用用户指定的地址，没指定就用清单里的官方名。
func imageOrDefault(custom, fallback string) string {
	if strings.TrimSpace(custom) != "" {
		return custom
	}
	return fallback
}
