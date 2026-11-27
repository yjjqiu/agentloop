package main

import (
	"os"
	"strings"
)

// version 是 daemon 的版本号，发布时用 -ldflags "-X main.version=x.y.z" 覆盖，
// 本地开发默认跟随仓库版本。
var version = "0.4.0"

// defaultRepo 是发布 / 更新检查用的 GitHub 仓库（owner/repo），
// 可用环境变量 AGENTDECK_REPO 覆盖。
const defaultRepo = "yjjqiu/agentloop"

// repoSlug 返回当前生效的仓库标识。
func repoSlug() string {
	if v := strings.TrimSpace(envOr("AGENTDECK_REPO", "")); v != "" {
		return v
	}
	return defaultRepo
}

// envOr 读取环境变量，空值时返回默认值。
func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
