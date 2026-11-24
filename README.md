# AgentDeck

把 Claude Code、Codex CLI、Gemini CLI 等命令行 Agent 放进同一个工作台：一个看板跑六阶段工作流，
每个阶段换不同的 CLI，终端、产物、交接记录都留在同一个界面里。

- **Windows**：Electron 桌面版，双击即用（免安装版）
- **Linux 服务器**：Go daemon + 浏览器访问，systemd 或 Docker 部署

![版本](https://img.shields.io/github/v/release/yjjqiu/agentloop?label=version)

## 组成

| 目录 | 说明 |
|------|------|
| `agentdeckd/` | Go 写的守护进程：PTY 会话、工作流编排、HTTP API、内嵌整个界面（`ui/` 随二进制嵌入） |
| `agentdeck/` | Electron 外壳：拉起 daemon、开窗口、原生对话框；打包成 Windows 便携版 |
| `.github/workflows/release.yml` | 打 tag 自动构建 Windows 便携版 / Linux 二进制 / Docker 镜像并发布 |

daemon 负责一切，外壳只负责窗口。所以：

- 关掉窗口，终端会话还在（重开自动接回）
- 界面本来就是普通网页，服务器上不用装桌面环境，浏览器直接开
- Windows 版和 Linux 版共用同一份前端和 API

## 快速开始

### Windows

1. 从 [Releases](https://github.com/yjjqiu/agentloop/releases) 下载 `AgentDeck-<版本>-portable.exe`
2. 双击运行（首次会在 `%APPDATA%\agentdeckd` 建配置）

源码运行：

```bash
cd agentdeckd && go build -o agentdeckd.exe .    # 需要 Go 1.27+
cd ../agentdeck && npm install && npm start
```

### Linux（systemd）

```bash
# 下载对应架构的包（amd64 / arm64）
tar -xzf agentdeckd-<版本>-linux-amd64.tar.gz
cd agentdeckd-<版本>-linux-amd64
sudo ./deploy/install.sh            # 建用户、装服务、生成访问令牌、开机自启
# 如果从 git 克隆（文件没有可执行位），用 sudo bash deploy/install.sh
```

脚本会打印访问地址和令牌，浏览器打开 `http://服务器:3210`，输入令牌即可。
之后可以在「设置 → CLI」里让 AgentDeck 安装各家 CLI，或自己 `sudo -u agentdeck npm i -g ...`。

### Linux（Docker）

```bash
cd agentdeckd
export AGENTDECK_TOKEN=换成足够长的随机串
docker compose up -d --build
```

详见 `agentdeckd/docker-compose.yml`。生产环境建议前面加 Nginx + HTTPS：

```bash
sudo cp agentdeckd/deploy/nginx-agentdeck.conf /etc/nginx/conf.d/agentdeck.conf
```

## 访问令牌（鉴权）

- 默认只监听 `127.0.0.1`，本机不需要令牌
- 监听 `0.0.0.0` 时自动启用鉴权；没有指定令牌会自动生成并写进配置（启动日志里能看到）
- 手动指定：`-token xxx`、环境变量 `AGENTDECK_TOKEN`、或配置文件里的 `authToken`
- 浏览器首次访问弹出登录框，验证后写 30 天 HttpOnly Cookie；接口也接受
  `Authorization: Bearer <token>` 和 `X-AgentDeck-Token`

**注意**：界面里启动的 Agent 会以服务用户身份执行命令（看板默认带
`--dangerously-skip-permissions`）。只把端口开放给可信网络，公网务必用 HTTPS。

## 版本与更新

版本号单一来源是 `agentdeckd/version.go` 的 `var version`；发版时用 tag `vX.Y.Z` 注入到二进制和
Windows 便携版。

应用内更新（设置 → 关于 / 版本与更新）：

1. daemon 定时查 GitHub Releases 最新正式版（30 分钟缓存，可点「检查更新」强制刷新）
2. 有新版本时，标题栏出现「vX.Y.Z 可用」徽标
3. 点开「关于」可以直接下载对应平台的安装包

更新方式：

- **Windows**：下载新的 `AgentDeck-<版本>-portable.exe`，退出旧进程后替换文件再启动；配置和会话都在 `%APPDATA%\agentdeckd`，不会丢
- **Linux**：替换 `agentdeckd` 二进制后 `sudo systemctl restart agentdeckd`；或 `docker compose pull && docker compose up -d`

**仓库是私有的**（当前默认）：GitHub 匿名 API 会返回 404，需要给检查更新配一个只读令牌——
在「设置 → 关于 → 私有仓库 Token」里粘贴 Fine-grained token（只授权本仓库
`Contents: Read-only`），或给服务进程设环境变量 `GITHUB_TOKEN`（环境变量优先）。
令牌保存在 `config.json`（权限 0600），界面只显示是否已配置，不回显内容。

发布流程见 [RELEASE.md](RELEASE.md)。

## 文档

- `agentdeckd/README.md`：daemon 代码结构、HTTP API、阶段编排、界面规范
- `agentdeck/README.md`：Electron 外壳与打包
- `RELEASE.md`：发版、CI、产物命名约定

## 许可

MIT，见 [LICENSE](LICENSE)。