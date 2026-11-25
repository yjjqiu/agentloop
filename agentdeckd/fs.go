package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// fsEntry 是目录选择里的一个子目录。
type fsEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// fsListing 是 GET /api/v1/fs/list 的返回。
type fsListing struct {
	Path   string    `json:"path"`
	Parent string    `json:"parent,omitempty"`
	Home   string    `json:"home,omitempty"`
	Dirs   []fsEntry `json:"dirs"`
	// Roots 是快捷入口（工作区 + 挂载进来的宿主机目录），界面顶部显示成按钮。
	Roots []fsEntry `json:"roots,omitempty"`
}

// registerFsRoutes 注册目录浏览接口，供浏览器模式选择项目/工作目录。
// 桌面端有原生选择框（shellApi.pickDir），不需要走这里。
func (a *App) registerFsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/fs/list", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" {
			path = strings.TrimSpace(a.config.LastCwd)
			if path == "" {
				path, _ = os.UserHomeDir()
			}
		}
		listing, err := listDirs(path)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		listing.Roots = rootShortcuts()
		writeJSON(w, http.StatusOK, listing)
	})

	mux.HandleFunc("POST /api/v1/fs/mkdir", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Parent string `json:"parent"`
			Name   string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "参数格式错误")
			return
		}
		parent := strings.TrimSpace(body.Parent)
		name := strings.TrimSpace(body.Name)
		if parent == "" || name == "" {
			writeError(w, http.StatusBadRequest, "缺少父目录或名称")
			return
		}
		// 只允许新建一级目录，名字里不能带路径分隔符。
		if name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
			writeError(w, http.StatusBadRequest, "文件夹名称不能包含路径分隔符")
			return
		}
		target := filepath.Join(filepath.Clean(parent), name)
		if err := os.Mkdir(target, 0o755); err != nil {
			if os.IsExist(err) {
				writeError(w, http.StatusBadRequest, "同名目录已存在")
				return
			}
			writeError(w, http.StatusBadRequest, "创建失败："+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": target})
	})
}

// listDirs 列出 path 下的子目录（只列目录，按名字排序）。
func listDirs(path string) (fsListing, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return fsListing{}, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fsListing{}, fmt.Errorf("目录不存在：%s", abs)
		}
		if os.IsPermission(err) {
			return fsListing{}, fmt.Errorf("没有权限读取：%s", abs)
		}
		return fsListing{}, err
	}

	dirs := make([]fsEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// 隐藏目录（.git、.cache 之类）不列出来，避免刷屏；需要时直接输路径。
		if strings.HasPrefix(name, ".") {
			continue
		}
		dirs = append(dirs, fsEntry{Name: name, Path: filepath.Join(abs, name)})
	}
	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})

	listing := fsListing{Path: abs, Dirs: dirs}
	if parent := filepath.Dir(abs); parent != abs {
		listing.Parent = parent
	}
	listing.Home, _ = os.UserHomeDir()
	return listing, nil
}

// rootShortcuts 返回目录选择器的快捷入口：工作区，加上 AGENTDECK_ROOTS
// 里配置的宿主机目录（容器部署时把宿主项目挂进来，省得在一堆系统目录里找）。
// 格式：逗号分隔的绝对路径，例如 /workspace,/home/ubuntu/czlingfu-site。
func rootShortcuts() []fsEntry {
	candidates := []fsEntry{{Name: "工作区", Path: "/workspace"}}
	for _, raw := range strings.Split(os.Getenv("AGENTDECK_ROOTS"), ",") {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		candidates = append(candidates, fsEntry{Name: filepath.Base(path), Path: path})
	}

	roots := make([]fsEntry, 0, len(candidates))
	seen := map[string]bool{}
	for _, entry := range candidates {
		if seen[entry.Path] {
			continue
		}
		info, err := os.Stat(entry.Path)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[entry.Path] = true
		roots = append(roots, entry)
	}
	return roots
}
