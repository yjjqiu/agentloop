package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed all:ui
var uiFS embed.FS

// App 是 daemon 的根对象，持有配置和会话管理器。
type App struct {
	dataDir  string
	sessions *SessionManager
	config   *Config
	pipes    *PipelineManager
	versions *VersionChecker
	install  *CliInstaller
	releases *ReleaseService
	auth     *Auth
	updates  *UpdateService
}

func defaultDataDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "agentdeckd")
}

// cliList 返回新建终端和设置页共用的 CLI 列表（已应用本地覆盖）。
func (a *App) cliList() []map[string]any {
	overrides := a.config.CliOverridesSnapshot()
	agents := DetectAgentsWith(overrides)
	list := make([]map[string]any, 0, len(agents))
	for _, agent := range agents {
		ov := overrides[agent.ID]
		detectedPkg, _ := a.versions.PackageFor(agent.Bin)
		pkg := a.resolvePackage(agent, ov)
		list = append(list, map[string]any{
			"id":           agent.ID,
			"label":        agent.Name,
			"bin":          agent.Bin,
			"path":         agent.Path,
			"installed":    agent.Installed,
			"enabled":      agent.Enabled,
			"args":         agent.Args,
			"defaultArgs":  agent.DefaultArgs,
			"modes":        agent.Modes,
			"mode":         agent.Mode,
			"overridePath": ov.Path,
			"overrideArgs": ov.Args,
			"overrideMode": ov.Mode,
			"command":      CliCommand(agent),
			"note":         agent.Note,
			"packageName":  pkg,
			"detectedPkg":  detectedPkg,
			"overridePkg":  ov.Package,
			"npmManaged":   a.versions.IsNpmManaged(agent.Path),
			"updateCmd":    strings.Join(agent.Update, " "),
			"repo":         agent.Repo,
			"stages":       agent.Stages,
			"fitNote":      agent.FitNote,
		})
	}
	return list
}

// resolvePackage 决定一款 CLI 使用的 npm 包名：界面填写 > agents.json 默认 > 本机检测。
func (a *App) resolvePackage(agent AgentStatus, ov CliOverride) string {
	if pkg := strings.TrimSpace(ov.Package); pkg != "" {
		return pkg
	}
	if pkg := strings.TrimSpace(agent.Npm); pkg != "" {
		return pkg
	}
	if pkg, ok := a.versions.PackageFor(agent.Bin); ok {
		return pkg
	}
	return ""
}

func main() {
	addr := flag.String("addr", "127.0.0.1:3210", "监听地址")
	dataDir := flag.String("data", defaultDataDir(), "配置目录")
	uiDir := flag.String("ui", "", "从目录提供界面（开发用，改完刷新即可）")
	tokenFlag := flag.String("token", "", "访问令牌（对外监听时启用鉴权；留空读 AGENTDECK_TOKEN 或配置文件）")
	showVersion := flag.Bool("version", false, "打印版本号后退出")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("创建配置目录失败: %v", err)
	}

	app := &App{dataDir: *dataDir, sessions: NewSessionManager()}
	app.config = LoadConfig(*dataDir)
	app.pipes = NewPipelineManager(app)
	app.versions = NewVersionChecker()
	app.install = NewCliInstaller(app.config, app.versions)
	app.releases = NewReleaseService(*dataDir)
	app.updates = NewUpdateService(repoSlug(), app.config.GitHubTokenValue)

	// 鉴权：-token / 环境变量总是生效；配置文件里的令牌只在对外监听时使用
	// （本机监听还要求登录会打断桌面版，没必要）。
	token := strings.TrimSpace(*tokenFlag)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("AGENTDECK_TOKEN"))
	}
	expose := !isLoopbackAddr(*addr)
	if token == "" && expose {
		token = app.config.AuthTokenValue()
	}
	if token == "" && expose {
		if generated := randomToken(); generated != "" {
			token = generated
			app.config.SetAuthToken(generated)
			log.Printf("已生成访问令牌（也可用 -token 指定）: %s", generated)
		}
	}
	app.auth = NewAuth(token)
	if app.auth.Enabled() {
		log.Printf("访问鉴权已启用：浏览器首次访问需要输入令牌")
	}

	mux := http.NewServeMux()
	app.routes(mux)
	app.auth.Register(mux)

	if *uiDir != "" {
		log.Printf("界面从目录提供: %s", *uiDir)
		mux.Handle("/", noStore(http.FileServer(http.Dir(*uiDir))))
	} else {
		static, err := fs.Sub(uiFS, "ui")
		if err != nil {
			log.Fatalf("加载界面资源失败: %v", err)
		}
		// 内置资源同样禁止缓存：界面是随二进制一起发的，换版本就是换界面，
		// 一旦被浏览器或 CDN 缓存住，用户会一直看到旧界面。
		mux.Handle("/", noStore(http.FileServer(http.FS(static))))
	}

	// 未开令牌（本机监听）时额外校验 Host，防 DNS rebinding。
	guarded := http.Handler(app.auth.Middleware(mux))
	if !app.auth.Enabled() {
		guarded = localHostGuard(guarded)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           withLogging(withCORS(guarded)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("agentdeckd %s listening on http://%s", version, *addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func homeDir() string {
	dir, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return dir
}
