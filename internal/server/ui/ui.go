// Package ui 通过 go:embed 把前端构建产物嵌进服务端二进制，实现单文件部署。
// dist 内容由 `make web`（vite build）生成；仓库中仅保留 dist/.gitkeep 占位，
// 保证未构建前端时 `go build` 也能通过。
package ui

import "embed"

// Dist 返回前端静态资源（根为 dist 目录）。
//
//go:embed all:dist
var dist embed.FS

// Dist 导出嵌入资源。
func Dist() embed.FS { return dist }
