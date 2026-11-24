//go:build !windows

package main

import (
	"os"
	"strings"
)

// defaultLocalBin 在非 Windows 上没有约定位置，留空由用户在设置里填。
const defaultLocalBin = ""

// normalizeLocalBin 清掉误留的 Windows 路径（老版本把 `E:\LoopForge\...` 写进了配置）。
// 在 Linux/macOS 上这种路径本来就不存在，显示出来只会误导。
func normalizeLocalBin(path string) string {
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if len(path) >= 2 && path[1] == ':' {
		return ""
	}
	if strings.Contains(path, `\`) {
		return ""
	}
	return path
}
