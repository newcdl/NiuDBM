// Package webui 把前端构建产物打进二进制，最终只分发一个可执行文件。
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// Dist 返回前端产物根目录（dist 下的文件）。
func Dist() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		// dist 由 //go:embed 保证存在，走到这里说明嵌入结构被破坏
		panic("webui: 无法读取嵌入的前端资源: " + err.Error())
	}
	return sub
}
