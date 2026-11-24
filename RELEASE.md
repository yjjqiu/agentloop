# 发版流程

## 0. 首次推送到 GitHub

仓库根目录已经 `git init`，`.gitignore` 用白名单只跟踪 `agentdeck/`、`agentdeckd/` 和根文档，
其余本地目录不会进仓库（`git add -A --dry-run` 可以确认）。

```bash
cd E:\googledown
git add -A
git commit -m "chore: 初始化 AgentDeck 仓库（v0.3.0）"
git branch -M main
git remote add origin https://github.com/yjjqiu/agentloop.git
git push -u origin main
```

推完到仓库 Settings → Actions 确认 workflow 有写权限（默认 `GITHUB_TOKEN` 即可，
workflow 里已声明 `contents: write` / `packages: write`）。

## 1. 版本号规则

`MAJOR.MINOR.PATCH`（语义化版本）：

- **MAJOR**：不兼容改动（配置格式、HTTP API 破坏性变更）
- **MINOR**：新增功能，向后兼容
- **PATCH**：修 bug

版本号只有一个来源：`agentdeckd/version.go`

```go
var version = "0.3.0"
```

Windows 便携版的版本由 CI 读 tag 后写入 `agentdeck/package.json`，不需要手动改两处。
本地开发想手动打包时，记得同步 `agentdeck/package.json` 的 `version`。

## 2. 发版

```bash
# 1) 改版本号
#    agentdeckd/version.go: var version = "0.3.1"

# 2) 提交
git add -A
git commit -m "release: v0.3.1"

# 3) 打 tag 并推送（CI 从这里开始）
git tag v0.3.1
git push origin main
git push origin v0.3.1
```

CI 会校验 **tag 与 version.go 是否一致**，不一致直接失败——避免发出去的包版本对不上。

推送 tag 后 GitHub Actions 执行 `.github/workflows/release.yml`：

| Job | 产物 |
|-----|------|
| `desktop-windows` | `AgentDeck-<ver>-portable.exe`（Windows 免安装版） |
| `daemon-linux` | `agentdeckd-<ver>-linux-amd64.tar.gz`、`agentdeckd-<ver>-linux-arm64.tar.gz` |
| `docker` | `ghcr.io/<owner>/agentdeckd:<ver>`、`:latest`（amd64 + arm64） |
| `release` | 汇总以上文件 + `SHA256SUMS`，创建 GitHub Release |

## 3. 产物命名契约（应用内更新依赖它）

daemon 的更新检查（`agentdeckd/update.go`）读 GitHub 的 `releases/latest`；
前端（`ui/app.js` 的 `pickUpdateAsset`）按平台挑文件：

| 平台 | 匹配规则 |
|------|----------|
| Windows | 文件名以 `.exe` 结尾 |
| Linux amd64 | 文件名包含 `linux` 和 `amd64`（或 `x86_64`） |
| Linux arm64 | 文件名包含 `linux` 和 `arm64` |

所以**不要随意改产物命名**；改了要同步改 `pickUpdateAsset`。

## 4. 没有 CI 时手动发版

```bash
# Windows daemon + 便携版
cd agentdeckd
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=0.3.1" -o agentdeckd.exe .
cd ../agentdeck
npm pkg set version=0.3.1
npm run dist

# Linux
cd ../agentdeckd
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=0.3.1" -o agentdeckd .
tar -czf agentdeckd-0.3.1-linux-amd64.tar.gz agentdeckd deploy README.md

# 发布
gh release create v0.3.1 \
  ../agentdeck/dist/AgentDeck-0.3.1-portable.exe \
  agentdeckd-0.3.1-linux-amd64.tar.gz \
  --title "AgentDeck v0.3.1" --generate-notes
```

## 5. 验证更新检查

```bash
# 看当前版本、仓库、平台
curl -s http://127.0.0.1:3210/api/v1/health

# 强制刷新更新检查（跳过 30 分钟缓存）
curl -s "http://127.0.0.1:3210/api/v1/update/check?force=1"
```

正常返回示例：

```json
{"current":"0.3.0","latest":"0.3.1","available":true,
 "url":"https://github.com/yjjqiu/agentloop/releases/tag/v0.3.1",
 "assets":[{"name":"AgentDeck-0.3.1-portable.exe","url":"..."}]}
```

## 6. 仓库设置注意

- **仓库需要公开**：daemon 匿名调用 GitHub API 查最新版；私有仓库会返回 404。
  若必须私有，给服务进程配只读 `GITHUB_TOKEN`（`agentdeckd/update.go` 已支持）。
- **GHCR 镜像**：workflow 已带 `packages: write`；首次推送后到仓库 Packages 里把镜像可见性设为 public，
  否则服务器 `docker pull` 需要登录。
- **Windows 便携版未签名**：首次运行 SmartScreen 会提示，属正常现象；要做正式分发可加代码签名证书。

## 7. 手动触发

Actions 页面选 `release` → Run workflow → 输入版本号（不带 `v`）。
注意 tag 不存在时 `action-gh-release` 会创建 tag，所以手动触发前确认版本号没写错。