# agentdeckd

AgentDeck 的本地守护进程：Go 写的小型服务（按职责拆成多个文件），带界面资源，监听 `127.0.0.1:3210`。

架构参照 OrchestratorInc/agent-orchestrator：**daemon 负责一切（PTY、工作流扫描、静态界面），Electron 只当外壳**。

## 为什么拆成 daemon

- 关掉窗口，终端会话还在（重开自动接回，靠 256KB 输出回放）
- 界面就是普通网页，浏览器也能直接开 `http://127.0.0.1:3210`
- Go 管进程/PTY/并发比 Node 更稳，打包只有一个 11MB exe

## 运行

```bash
go build -o agentdeckd.exe .
./agentdeckd.exe                      # 默认 127.0.0.1:3210
./agentdeckd.exe -addr 127.0.0.1:3300 -data D:\deck-data
./agentdeckd.exe -addr 0.0.0.0:3210 -token <访问令牌>   # 对外监听，浏览器要登录
./agentdeckd.exe -version             # 打印版本号
```

编译环境：Go 1.27+（本机装在 `E:\googledown\toolchain\go`，`GOPROXY=https://goproxy.cn,direct`）。
Windows / Linux / macOS 都能编译：PTY 按平台拆在 `platform_windows.go`（ConPTY）和
`platform_unix.go`（creack/pty）。

## 代码结构

按职责分文件，新增接口 / 阶段逻辑时按这张表找位置：

| 文件 | 职责 |
|------|------|
| `main.go` | 入口、`App` 结构、监听、静态界面（`ui/` 随 exe 嵌入） |
| `main_routes.go` | 全部 HTTP 路由，按系统 / 项目 / CLI / 流水线 / 会话分组注册 |
| `pipeline.go` | 阶段常量与元数据、`PipelineRunView`、`PipelineManager`、主循环 |
| `pipeline_lifecycle.go` | 启动 / 停止 / 恢复 / 打回、运行租约（90s TTL / 25s 续期） |
| `pipeline_dispatch.go` | 派发阶段终端、复用会话、注入提示、判断 TUI 已启动 |
| `pipeline_prompt.go` | 阶段提示词、角色规则文件、返工说明、上交接文件读取 |
| `pipeline_gate.go` | 产物门禁、交接文件六节校验 |
| `pipeline_state.go` | `workflow-state.json` 读写、阶段推进、归档、工作区创建 |
| `sessions.go` | PTY 会话管理、SSE 流、tail 预览 |
| `agents.go` | `agents.json` 解析与 CLI 探测（一次扫描 PATH 的索引） |
| `workflows.go` | LoopForge 工作流扫描与本机安装状态 |
| `config.go` / `versions.go` / `releases.go` / `install.go` 等 | 配置持久化、版本检查、更新说明拉取、npm 安装任务 |
| `version.go` | 版本号（`var version`，发版用 ldflags 覆盖）与发布仓库 `yjjqiu/agentloop` |
| `update.go` | 查 GitHub Releases 最新版、版本比较、30 分钟缓存 |
| `auth.go` | 对外监听的令牌鉴权（Bearer / Cookie / 查询参数） |
| `platform_windows.go` / `platform_unix.go` | PTY 启动、命令包装、PATH 扩展等平台差异 |

前端在 `ui/`：`index.html` + `app.js`（外壳 / 终端 / 设置）+ `board.js`（看板与阶段流程）
+ `avatars.js`（品牌头像），样式 `app.css` / `board.css`。

## 界面主题

外壳默认浅色路线图风格，设置 → 外观分两组配色：

- **软件配色**：9 套整包配色，分浅色 / 夜间两组。
  - 浅色：紫罗兰 / 海盐蓝 / 苔原绿 / 暖陶橙 / 樱花粉 / 石墨灰。
  - 夜间：深空（中性深色）/ 午夜蓝 / 松林夜。夜间配色会同时设置 `color-scheme: dark`，
    原生滚动条、输入框、下拉框一起转深色。
  - 画布、卡片表面、边框、强调色、语义色（绿/琥珀/红）一起切换（各级表面色都带同色系
    微着色），通过 `:root[data-app-theme]` 的 CSS 令牌生效；选择存进 `localStorage`，
    刷新和重启后保留。
- **终端配色**：6 套终端配色（暗夜 / 石墨 / 紫夜 / 深海 / 暖沙 / 纸白），只改终端
  内容区，切换后即时应用到所有已打开终端；「纸白」是浅色底，适合白天亮屏，个别
  TUI 的低对比前景色可能偏淡。终端内容与软件配色互不影响。

## 界面设计规范

前端全部是原生 CSS，规范写在 `ui/app.css` 的令牌和组件注释里，新增界面按下面几条来：

- **颜色只取令牌**：`--bg` / `--surface*` / `--line*` / `--text*` / `--accent*` /
  `--green|--amber|--red` 及其 `-rgb` 变体；不要在组件里写色值，否则夜间模式会出现
  浅色残块。
- **按钮只有一个来源**：`.btn` 统一定义高度（32px）、内边距、圆角、字号、1px 边框和
  hover/active/focus-visible/disabled 状态；尺寸只用 `.mini`（28px），变体只用
  `.primary` / `.ghost` / `.danger`。页面里不要再覆盖 `height` / `padding` / `border`，
  同一排的按钮因此天然对齐；文字会变的按钮用 `min-width` 防止整排抖动。
- **圆角与间距**：卡片/面板 14px，普通控件 8px，mini 控件 6px；同组间距 6–10px，
  区块内边距 12–18px。
- **动效只改 transform / opacity**，时长 120–300ms；`prefers-reduced-motion` 下关闭。
- **轮询只做原地更新**：看板每 2 秒刷新一次，已存在的卡片/节点不要重复 `appendChild`
  或重建 innerHTML——浏览器会把重新插入当成新节点，入场动画每 2 秒重放一次，
  看起来就是「已完成的任务一直在跳」。只在换泳道、换顺序或状态真正变化时改 DOM。
- **CLI 一律显示品牌头像**：阶段下拉切换 CLI 时，左侧头像和流程图上的跑者头像立即
  跟着换（不刷新页面），规则见下一节。
- **版本与状态信息用「标签 + 值」网格**：设置 → 关于用 `.about-grid`（10.5px 次要标签 +
  12px mono 值），不在正文里堆句子；「有新版」徽标只在真的检测到新版本时出现，
  点击直接跳到「关于」，不要做成常驻入口。

## 品牌动态头像

终端、阶段流程图和看板卡片上都会显示对应 CLI 的品牌头像（`ui/avatars.js`）：

- 徽章是内联 SVG：31 款 CLI 用官方 logo（`ui/brand-logos.js`，来自 simple-icons /
  Iconify 的 CC0·MIT 图标集，少数取自厂商站点 favicon），白底圆片 + 原色 logo；
  其余没有公开 logo 的用品牌色 + 首字母 mono 兜底，颜色按 id 稳定散列——同一个 CLI
  在哪儿都是同一个徽章。
- 状态直接用动效表达：运行中轻微跳动 + 脉冲光环，等待你输入是琥珀色呼吸环，
  失败是红色实环，已完成是绿色淡环，未开始不显示光环。
- 出现在：左侧终端列表、终端标题栏、看板卡片的当前阶段、下方阶段流程条（每个阶段
  一个）、右侧工作流阶段图的每个节点。
- 阶段流程图右上角还有一个「跑者」头像：阶段推进时它从上一个节点跳到下一个节点，
  暂停 / 出错 / 完成时换成对应状态色，让看板跟着进度动起来。
- 会话的 CLI 身份由 daemon 记录（`/api/v1/sessions` 的 `agent` 字段）：流水线派发时
  写入 CLI id；手动新建终端时前端按启动命令里的可执行文件名匹配 `agents.json`。
- 动效只改 transform / opacity，并在 `prefers-reduced-motion` 下自动关闭。

新增界面颜色一律使用 `ui/app.css` `:root` 里的令牌（`--bg`、`--surface*`、`--line*`、
`--text*`、`--accent*`、`--green/--amber/--red` 及其 `-rgb` 变体、`--track/--scroll/--overlay/--dot`
等），不要在组件里硬编码色值，否则夜间模式会出现浅色残块。

设置弹窗左侧是竖向页签（终端 CLI / 工作流 / 外观），右侧显示对应面板。

## CLI 设置页：阶段适配与更新内容

设置 → 终端 CLI 里，每一行除了路径/参数/npm 包，还会显示这款 CLI 在流水线 6 个阶段
（需求 / 方案 / 开发 / 审查 / 测试 / 沉淀）上的适配度：

- 6 个热度格（需 方 开 审 测 沉）表示 0–5 星，悬停有具体分数；4 星以上算强项。
- 下一行直接写「最擅长 方案 · 开发」并附一句理由。
- 顶部一排阶段筛选：点「审查」只列审查 4 星以上的 CLI，并按分数排序；「全部」恢复。
- 数据在 `agents.json` 的 `stages`（键与 `pipeline.go` 的 `stageMeta` 一致）和 `fitNote`，
  依据公开评测与产品定位整理，属于参考值，可直接改 JSON 调整。

每行末尾的「更新内容」（点版本徽标也可以）会打开右侧抽屉，显示该 CLI 最新版本的
发布说明：先按 `agents.json` 的 `repo` 调 GitHub Releases，再把正文翻译成简体中文。
译文缓存在 `%APPDATA%\agentdeckd\changelog-zh.json`（按 `repo@tag` 键），同一版本只翻译
一次；翻译走公开的 MyMemory 接口（免费额度按天计算，超额时保留已译部分并提示切换
「英文原文」）。单次发布最多翻译 6KB 正文，更长的部分提示去发布页看全文。
没有收录 `repo` 的 CLI 会提示暂未收录发布源。

CLI 列表顶部有两个版本操作：

- **刷新版本**：强制重新探测本机版本和 npm 最新版（清掉 10 分钟缓存），一次刷新全部行。
- **一键更新 N**：把所有「可升级且能自动更新」的 CLI（npm 全局安装的走
  `npm install -g <包>@latest`，自管理的走它自己的 update 命令）按顺序逐个更新到最新版，
  显示 `更新中 2/5` 进度；跑完自动强制刷新版本号，并在标题栏给出「已全部更新到最新版」
  或「N 款失败：…」的汇总。已经有一个安装任务在跑时会拒绝插队，行内单款「更新」按钮保留。
  不是 npm 全局安装、也没有内置更新命令的 CLI 会跳过，按钮提示里会注明有几款需手动更新。

## HTTP API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/health` | 探活 `{status,version,repo,platform,auth,sessions}`；`auth=true` 表示要令牌 |
| GET | `/api/v1/update/check?force=1` | 最新正式版 `{current,latest,available,url,notes,assets[]}`，`force=1` 跳过缓存 |
| GET | `/api/v1/projects` | 项目列表 + 当前项目 |
| POST | `/api/v1/projects` | 添加项目 `{path}` |
| POST | `/api/v1/projects/remove` | 移除 `{path}` |
| POST | `/api/v1/projects/active` | 切当前项目 `{path}` |
| GET | `/api/v1/workflows?root=` | 扫 `artifacts/*/workflow-state.json` |
| GET/POST | `/api/v1/config` | 读取/保存 `lastCwd`、`localBin` |
| GET | `/api/v1/clis` | 探测注册表里的 44 款 CLI，返回 `{id,label,bin,path,installed,enabled,args,defaultArgs,overridePath,overrideArgs,overridePkg,packageName,detectedPkg,npmManaged,updateCmd,command,repo,stages,fitNote}`，已安装的排前面 |
| GET | `/api/v1/clis/release?id=&refresh=1` | 该 CLI 最新版本的发布说明 `{info:{repo,tag,version,title,publishedAt,url,body,zh,translated,note,error}}`，翻译结果缓存，`refresh=1` 重新拉取 |
| POST | `/api/v1/clis/config` | 保存单款 CLI 覆盖 `{id,path,args[],package,enabled?}`，返回 `{ok,clis}` |
| POST | `/api/v1/clis/reset` | 清除覆盖 `{id}`，返回 `{ok,clis}` |
| GET | `/api/v1/clis/versions?refresh=1` | 本机版本（`--version` 探测）+ npm 最新版，返回 `{versions:{id:{installed,latest,package,outdated,...}},checkedAt}`，缓存 10 分钟 |
| GET | `/api/v1/clis/job` | 当前安装 / 更新任务快照 `{job:{target,label,kind,running,ok,error,lines[],startedAt,doneAt}}`，没有任务时为 `null` |
| POST | `/api/v1/clis/install` | 用 npm 全局包安装或更新一款 CLI `{id}`，返回任务快照 |
| POST | `/api/v1/clis/job/cancel` | 取消正在进行的任务 |
| GET | `/api/v1/agents` | 同上，附完整 `AgentDef`（args / prompt 传递方式 / note） |
| GET | `/api/v1/pipeline?root=` | 项目的阶段→CLI 配置 + 所有流水线运行状态 |
| POST | `/api/v1/pipeline/config` | 保存阶段映射 `{root, stageAgents:{TASK-01:"claude-code",...}, auto?}` |
| POST | `/api/v1/pipeline/start` | 启动流水线 `{root, slug}` 或 `{root, requirement}`（自动建 artifacts） |
| POST | `/api/v1/pipeline/stop` | 停止调度 `{root, slug}`（不动已开的终端） |
| POST | `/api/v1/pipeline/resume` | 从暂停态继续 `{root, slug}`；门禁仍失败时会重新执行当前阶段 |
| POST | `/api/v1/pipeline/reject` | 人工打回 `{root, slug, stage, target, reason}`，退到任一上游阶段并继续驱动 |
| POST | `/api/v1/install` | 调 `loopforge-cli install <host>` `{root,host}` |
| GET | `/api/v1/sessions` | 会话列表（`title` 是流水线写入的「阶段 · 任务」标记，`agent` 是会话使用的 CLI id） |
| POST | `/api/v1/sessions` | 新建 PTY `{command,cwd,agent?}`，`agent` 可选，用于界面显示品牌头像 |
| POST | `/api/v1/sessions/{id}/input` | 写入输入 `{data}` |
| POST | `/api/v1/sessions/{id}/resize?cols=&rows=` | 改尺寸 |
| DELETE | `/api/v1/sessions/{id}` | 关闭会话 |
| GET | `/api/v1/sessions/{id}/stream` | SSE，`data` 为 base64 字节，`exit` 为退出 |
| GET | `/api/v1/sessions/{id}/tail?max=` | 最近的纯文本输出（去掉 ANSI），看板卡片实时预览用 |
| GET | `/api/v1/auth/status` | 鉴权状态 `{enabled,authenticated}` |
| POST | `/api/v1/auth/login` | 令牌登录 `{token}`，成功后写 30 天 HttpOnly Cookie |
| POST | `/api/v1/auth/logout` | 清 Cookie |

鉴权范围：除 `/api/v1/health`、`/api/v1/auth/*` 和静态界面外，所有 `/api/` 接口都要令牌；
未通过时返回 `401 {"error":"unauthorized","authRequired":true}`。

## 版本与更新检查

- 版本号：`version.go` 的 `var version = "0.3.0"`，发版时 `-ldflags "-X main.version=x.y.z"` 覆盖
- 更新源：GitHub Releases `releases/latest`，仓库 `yjjqiu/agentloop`（`AGENTDECK_REPO` 可覆盖）
- 比较：点分版本号逐段比较；只有正式发布（非 prerelease）参与
- 缓存：成功 30 分钟、失败 2 分钟；设置页「检查更新」带 `force=1`
- 限制：GitHub 匿名 API 有配额，容器 / 服务器上访问受限时可配只读 `GITHUB_TOKEN`
- 前端只负责展示和给下载链接，安装动作交给用户（替换文件 / 重启服务）

## 访问令牌

```bash
./agentdeckd -addr 0.0.0.0:3210 -token my-secret
```

优先级：`-token` / `AGENTDECK_TOKEN`（总是生效）> 配置文件 `authToken`（只在对外监听时生效）。
对外监听且都没有时，启动时自动生成 32 字节随机令牌并写入配置，日志里会打印一次。比较使用
`subtle.ConstantTimeCompare`；登录失败会延迟 400ms 减缓暴力尝试；HTTPS（含反代的
`X-Forwarded-Proto`）下 Cookie 自动带 `Secure`。

未启用令牌（本机监听且没指定）时，服务还会校验 `Host` 必须是 `127.0.0.1` / `localhost` / `[::1]`，
挡住 DNS rebinding 借浏览器调用本地接口；同时不再给跨域来源发 CORS 头（界面本来就同源）。

## Linux 部署

产物和脚本都在 `deploy/`：

| 文件 | 用途 |
|------|------|
| `Dockerfile` | 多阶段构建，运行阶段基于 `node:22-bookworm-slim`（自带 node/npm 便于装 CLI） |
| `docker-compose.yml` | 数据卷 + 工作区挂载 + `AGENTDECK_TOKEN` |
| `deploy/install.sh` | 一键装 systemd：建用户、装二进制、生成令牌、开机自启 |
| `deploy/agentdeckd.service` | 手装用的 unit 参考（路径 /opt/agentdeck、/var/lib/agentdeck） |
| `deploy/agentdeck.env.example` | 环境变量模板（令牌、仓库、PATH） |
| `deploy/nginx-agentdeck.conf` | HTTPS 反代，SSE 不缓冲 |

注意：容器 / 服务用户里要自己装 CLI（`npm i -g @anthropic-ai/claude-code` 等），
AgentDeck 的「CLI 设置」页也能装，装到 `NPM_CONFIG_PREFIX` 指向的目录。

## 阶段编排（看板）

`pipeline*.go` 按 `workflow-state.json` 推进 LoopForge 的 6 个阶段。默认是**驱动模式**：

- 每个阶段派发都独立开一个 PTY 进程，角色身份靠启动参数注入的角色规则文件成立；
  跨阶段不共享对话历史，上下文只走 `artifacts/<slug>/` 这块黑板
- 阶段指令在 TUI 启动完成后自动输入（文本和回车分两次写，避免被吞），内容包含：
  角色约束（项目 / 本地 / 用户规则 + 内置兜底）、LoopForge edition 提示、上游输入文件、
  上一阶段交接摘要、返工打回包，以及「只写本阶段状态」和交接文件输出要求
- 阶段完成时写 `artifacts/<slug>/.agentdeck/handoff/<STAGE>.md`（六节：inputs_used /
  outputs / decisions / assumptions / open_questions / next_focus），终端输出归档到
  `.agentdeck/stages/<STAGE>.log` 后关闭进程
- 产物门禁：阶段声称完成但 `artifact_path` 或预期目录里没有非空 Markdown 时暂停，不推进
- 交接门禁：本阶段还要写六节齐全的 `handoff/<STAGE>.md`；缺失时暂停，避免下一阶段在
  没有上下文的情况下开工
- 产物新鲜度：被打回过的阶段，重跑后的产物 mtime 必须晚于本次派发时间，否则暂停，
  防止旧产物冒充返工结果
- 人工打回：看板上可把当前阶段退到任一上游阶段并填写原因，原因会写进下一轮返工提示；
  打回同时归档被退阶段日志、清空目标阶段的上次结论，然后继续驱动
- 门禁暂停后点「继续」：先重新检查门禁，仍失败就重跑当前阶段（确认只消费一次，
  重跑后门禁再失败会再次暂停，不会无限自动重跑）
- CODE-REVIEW / TASK-04 失败时沿回退边打回 TASK-03，最多 `maxReworks` 轮（默认 2）；
  同一阶段返工时若原终端还活着就复用，否则新开进程，达到上限后暂停等人工
- `auto=false` 时在 TASK-02 / CODE-REVIEW / TASK-04 三个门禁点暂停等人工确认
- 运行租约：驱动中的任务在 `.agentdeck/lease.json` 写 90 秒租约并每 25 秒续期；
  另一个 AgentDeck 实例正在驱动同一任务时拒绝启动，可显式「强制接管」覆盖
- 项目装了 LoopForge 时自动识别 edition（`classic` 的各阶段命令 / `portable` 的
  `/devflow`），并写进阶段提示；`workflow-state.json` 的 `runtime_mode` 不是
  `agentdeck` 时拒绝启动，需要显式「强制接管」

`agents.json` 里有 44 款 CLI 的描述（id、名称、可执行文件名、默认参数、prompt 是位置参数还是启动后注入），
来源是 OrchestratorInc/agent-orchestrator 的 adapter 定义，改这个文件就能增删 CLI。
新增的可选字段：`npm` 是安装 / 更新用的默认 npm 包名，`update` 是自更新命令的参数（如 pi 的 `["update"]`）。

## PTY

Windows 走 ConPTY（`github.com/UserExistsError/conpty`）。`.exe` 直接启动，`.cmd`/`.bat`/裸命令用 `cmd.exe /c` 包一层——所以 `claude`、`codex` 这类 npm shim 也能正常跑 TUI。

Linux / macOS 走 `creack/pty`：`/bin/sh -c "exec <命令>"` 起进程，`TERM=xterm-256color`，
窗口尺寸用 `TIOCSWINSZ` 同步。两边都实现 `ptyHost` 接口（`sessions.go`），上层代码不感知平台。

## 配置

`%APPDATA%\agentdeckd\config.json`（Linux 是 `~/.config/agentdeckd/config.json`，容器里是 `/data/config.json`）：

```json
{
  "projects": ["E:/path/to/project"],
  "activeProject": "E:/path/to/project",
  "lastCwd": "E:/path/to/project",
  "localBin": "E:\\LoopForge\\local\\bin\\loopforge.mjs",
  "authToken": "对外监听时自动生成的访问令牌",
  "cliOverrides": {
    "claude-code": { "path": "C:\\Users\\me\\AppData\\Roaming\\npm\\claude.cmd", "args": ["--dangerously-skip-permissions"] },
    "aider": { "enabled": false, "package": "aider-chat" }
  }
}
```

`cliOverrides` 就是界面里「终端 CLI 设置」写下的内容：`path` 指定可执行文件，
`args` 覆盖默认参数（空数组 = 沿用内置参数），`package` 是安装 / 更新用的 npm 包名，
`enabled:false` 表示停用——新建终端里置灰，流水线调度到它时直接暂停并提示。

CLI 探测走内建的路径索引（`agents.go` 的 `pathIndex`）：`PATH` 和各兜底目录每个只读一次，
再按 `PATHEXT` 匹配，44 款 CLI 一次探测在毫秒级；逐个 `exec.LookPath` 在 Windows 上要 2 秒以上。

版本探测（`versions.go`）分两步：先跑 `<cli> --version`（4 秒超时、并发 8）拿本机版本，
再按全局 `node_modules` 里的 `bin → package.json` 映射查 npm 最新版
（`registry.npmmirror.com` 优先，失败回退 `registry.npmjs.org`）。
所有结果缓存 10 分钟，改路径/参数或点「检查版本」会重新探测。

## CLI 安装 / 更新（`cli_install.go`）

设置页每行的「安装 / 更新」按钮走 daemon 的后台任务，思路和 magpie 自我更新一致：取官方最新包 → 替换本机 → 复查版本。

- **包名解析**：界面填写的 `cliOverrides[id].package` > `agents.json` 的 `npm` 字段 > 全局 `node_modules`
  里探测到的 `bin → package` 映射。未安装的 CLI 没有包名时，前端先让用户填一个再装。
- **任务**：一次只跑一个任务，输出逐行流式写进 `CliJob.Lines`（去掉 ANSI、`\r` 进度行只留最后一段，最多 200 行）。
  - npm 全局安装的 CLI：`npm install -g <包名>@latest --no-fund --no-audit`。
  - 自管理安装的（`npmManaged=false`，例如 pi 的 `~/.pi/agent/bin/pi`）：跑 `agents.json` 里的
    `update` 参数，例如 pi 的 `pi update`，避免 npm 更新到另一份副本。
  - 两者都不满足时返回错误，前端按钮置灰。
  Windows 上先把工作目录切到可执行文件所在目录再按文件名调用，避开 `cmd /c` 剥引号导致
  `C:\Program Files\nodejs` 这类带空格路径失败。
- **取消**：`context` 取消会结束子进程，任务标记为「已取消」。
- **收尾**：成功后清空版本缓存，前端重新拉 `clis` 和 `clis/versions`，能看到新版本。
- **非 npm 安装**：`npmManaged` 为 false 且没有 `update` 命令的 CLI 按钮置灰；
  像 pi 这样自带更新命令的走 `pi update`，更新的是实际在用的那份。