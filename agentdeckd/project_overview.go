package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// project_overview.go 提供「项目资料」看板的数据：文档、任务产物、目录结构、依赖清单与 Git 状态。
// 只读，不写入任何文件。

const (
	overviewMaxDocs     = 300
	overviewMaxDepth    = 4
	overviewReadMaxSize = 512 * 1024 // 预览单文件上限
)

// overviewDoc 是一条可预览的资料条目。
type overviewDoc struct {
	Path    string `json:"path"` // 绝对路径
	RelPath string `json:"rel"`  // 相对项目根
	Name    string `json:"name"`
	Group   string `json:"group"` // 根目录 / docs / artifacts / 其它一级目录
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
}

// overviewCommit 是一条 Git 提交。
type overviewCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	When    string `json:"when"`
}

// overviewGit 是项目仓库状态。
type overviewGit struct {
	Branch  string           `json:"branch"`
	Dirty   int              `json:"dirty"`
	Changed []string         `json:"changed"`
	Commits []overviewCommit `json:"commits"`
}

// overviewArtifact 是一个流水线任务产物目录。
type overviewArtifact struct {
	Slug         string            `json:"slug"`
	CurrentStage string            `json:"currentStage"`
	LastEvent    string            `json:"lastEvent"`
	RuntimeMode  string            `json:"runtimeMode"`
	Stages       map[string]string `json:"stages"` // 阶段 → 状态
	UpdatedAt    int64             `json:"updatedAt"`
}

// overviewEntry 是目录树的一项。
type overviewEntry struct {
	Name  string `json:"name"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size,omitempty"`
	Count int    `json:"count,omitempty"`
}

// projectOverview 是「资料」页的完整数据。
type projectOverview struct {
	Root      string             `json:"root"`
	Name      string             `json:"name"`
	Git       *overviewGit       `json:"git,omitempty"`
	Docs      []overviewDoc      `json:"docs"`
	Manifests []overviewDoc      `json:"manifests"`
	Artifacts []overviewArtifact `json:"artifacts"`
	Tree      []overviewEntry    `json:"tree"`
	ScannedAt int64              `json:"scannedAt"`
}

// overviewSkipDirs 是扫描时要跳过的重目录。
var overviewSkipDirs = map[string]bool{
	".git": true, "node_modules": true, "venv": true, ".venv": true,
	"__pycache__": true, ".next": true, "dist": true, "build": true,
	".idea": true, ".vscode": true, "target": true, ".cache": true,
	"site-packages": true, ".pytest_cache": true, "coverage": true,
}

// manifestNames 是根目录下值得单独列出的清单文件。
var manifestNames = []string{
	"README.md", "AGENTS.md", "CLAUDE.md", "package.json", "requirements.txt",
	"pyproject.toml", "go.mod", "Cargo.toml", "Makefile", "Dockerfile",
	"docker-compose.yml", "compose.yaml", "ecosystem.config.js",
}

// registerOverviewRoutes 注册项目资料看板与文件预览接口。
func (a *App) registerOverviewRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/overview", func(w http.ResponseWriter, r *http.Request) {
		root, err := a.projectRoot(r.URL.Query().Get("root"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, buildOverview(root))
	})

	mux.HandleFunc("GET /api/v1/projects/file", func(w http.ResponseWriter, r *http.Request) {
		path, err := a.projectFile(r.URL.Query().Get("path"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			writeError(w, http.StatusNotFound, "文件不存在")
			return
		}
		if info.Size() > overviewReadMaxSize {
			writeError(w, http.StatusBadRequest, "文件过大，无法预览（上限 512KB）")
			return
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "读取失败: "+err.Error())
			return
		}
		if looksBinary(raw) {
			writeError(w, http.StatusBadRequest, "二进制文件不支持预览")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":    path,
			"name":    filepath.Base(path),
			"size":    info.Size(),
			"modTime": info.ModTime().UnixMilli(),
			"content": string(raw),
		})
	})
}

// projectRoot 校验 root 必须是已登记的项目目录。
func (a *App) projectRoot(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", os.ErrInvalid
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	for _, p := range a.config.ProjectsSnapshot() {
		if filepath.Clean(p) == abs {
			if info, err := os.Stat(abs); err == nil && info.IsDir() {
				return abs, nil
			}
			return "", fs.ErrNotExist
		}
	}
	return "", errProjectNotRegistered
}

// errProjectNotRegistered 表示请求的目录不是当前登记的项目。
var errProjectNotRegistered = &projectError{"不是已登记的项目目录"}

type projectError struct{ msg string }

func (e *projectError) Error() string { return e.msg }

// projectFile 校验文件必须落在某个已登记项目内。
func (a *App) projectFile(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errProjectNotRegistered
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	for _, p := range a.config.ProjectsSnapshot() {
		rel, err := filepath.Rel(filepath.Clean(p), abs)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			continue
		}
		return abs, nil
	}
	return "", &projectError{"文件不在已登记的项目目录内"}
}

// buildOverview 扫描项目目录生成资料看板数据。
func buildOverview(root string) projectOverview {
	out := projectOverview{
		Root:      root,
		Name:      filepath.Base(root),
		Docs:      []overviewDoc{},
		Manifests: []overviewDoc{},
		Artifacts: []overviewArtifact{},
		Tree:      []overviewEntry{},
		ScannedAt: time.Now().UnixMilli(),
	}
	out.Git = readGit(root)
	out.Docs = collectDocs(root)
	for _, name := range manifestNames {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || info.IsDir() {
			continue
		}
		out.Manifests = append(out.Manifests, overviewDoc{
			Path: filepath.Join(root, name), RelPath: name, Name: name,
			Group: "清单", Size: info.Size(), ModTime: info.ModTime().UnixMilli(),
		})
	}
	out.Artifacts = collectArtifacts(root)
	out.Tree = readTree(root)
	return out
}

// collectDocs 收集 .md / .markdown 资料，按一级目录分组。
func collectDocs(root string) []overviewDoc {
	docs := make([]overviewDoc, 0, 64)
	rootDepth := strings.Count(root, string(filepath.Separator))
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && overviewSkipDirs[strings.ToLower(d.Name())] {
				return filepath.SkipDir
			}
			if strings.Count(path, string(filepath.Separator))-rootDepth >= overviewMaxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if len(docs) >= overviewMaxDocs {
			return filepath.SkipAll
		}
		name := strings.ToLower(d.Name())
		if !strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".markdown") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		docs = append(docs, overviewDoc{
			Path:    path,
			RelPath: filepath.ToSlash(rel),
			Name:    d.Name(),
			Group:   docGroup(rel),
			Size:    info.Size(),
			ModTime: info.ModTime().UnixMilli(),
		})
		return nil
	})
	sort.SliceStable(docs, func(i, j int) bool {
		if docs[i].Group != docs[j].Group {
			if docs[i].Group == "根目录" {
				return true
			}
			if docs[j].Group == "根目录" {
				return false
			}
			return docs[i].Group < docs[j].Group
		}
		if docs[i].RelPath != docs[j].RelPath {
			return docs[i].RelPath < docs[j].RelPath
		}
		return false
	})
	return docs
}

// docGroup 把相对路径映射成看板里的分组名。
func docGroup(rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) == 1 {
		return "根目录"
	}
	return parts[0]
}

// collectArtifacts 读取 artifacts/<slug>/workflow-state.json。
func collectArtifacts(root string) []overviewArtifact {
	base := filepath.Join(root, "artifacts")
	entries, err := os.ReadDir(base)
	if err != nil {
		return []overviewArtifact{}
	}
	out := make([]overviewArtifact, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		statePath := filepath.Join(base, entry.Name(), "workflow-state.json")
		raw, err := os.ReadFile(statePath)
		if err != nil {
			continue
		}
		var st struct {
			CurrentStage string                     `json:"current_stage"`
			LastEvent    string                     `json:"last_event"`
			RuntimeMode  string                     `json:"runtime_mode"`
			Stages       map[string]json.RawMessage `json:"stages"`
		}
		if err := json.Unmarshal(raw, &st); err != nil {
			continue
		}
		stages := make(map[string]string, len(st.Stages))
		for key, value := range st.Stages {
			var one struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(value, &one); err == nil && one.Status != "" {
				stages[key] = one.Status
			}
		}
		var updated int64
		if info, err := os.Stat(statePath); err == nil {
			updated = info.ModTime().UnixMilli()
		}
		out = append(out, overviewArtifact{
			Slug:         entry.Name(),
			CurrentStage: st.CurrentStage,
			LastEvent:    st.LastEvent,
			RuntimeMode:  st.RuntimeMode,
			Stages:       stages,
			UpdatedAt:    updated,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// readTree 读项目根的一级条目。
func readTree(root string) []overviewEntry {
	entries, err := os.ReadDir(root)
	if err != nil {
		return []overviewEntry{}
	}
	out := make([]overviewEntry, 0, len(entries))
	for _, entry := range entries {
		item := overviewEntry{Name: entry.Name(), Dir: entry.IsDir()}
		if entry.IsDir() {
			if sub, err := os.ReadDir(filepath.Join(root, entry.Name())); err == nil {
				item.Count = len(sub)
			}
		} else if info, err := entry.Info(); err == nil {
			item.Size = info.Size()
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// readGit 读分支、改动与最近提交；不是仓库时返回 nil。
func readGit(root string) *overviewGit {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return nil
	}
	run := func(args ...string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		// 中文 Windows 默认把 git 输出转成 GBK，并给非 ASCII 路径加引号转义；
		// 这里强制 UTF-8 且不加引号，界面才能正常显示中文提交信息和文件名。
		base := []string{"-C", root, "-c", "i18n.logOutputEncoding=UTF-8", "-c", "core.quotepath=false"}
		cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return string(out)
	}
	git := &overviewGit{Changed: []string{}, Commits: []overviewCommit{}}
	git.Branch = strings.TrimSpace(run("rev-parse", "--abbrev-ref", "HEAD"))
	for _, line := range strings.Split(strings.TrimRight(run("status", "--porcelain"), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		git.Dirty++
		if len(git.Changed) < 40 {
			git.Changed = append(git.Changed, strings.TrimSpace(line))
		}
	}
	logOut := run("log", "-8", "--pretty=%h%x09%ar%x09%s")
	for _, line := range strings.Split(strings.TrimRight(logOut, "\n"), "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		git.Commits = append(git.Commits, overviewCommit{SHA: fields[0], When: fields[1], Subject: fields[2]})
	}
	return git
}

// looksBinary 用前 512 字节里的 NUL 判断是否是二进制。
func looksBinary(raw []byte) bool {
	limit := len(raw)
	if limit > 512 {
		limit = 512
	}
	for i := 0; i < limit; i++ {
		if raw[i] == 0 {
			return true
		}
	}
	return false
}
