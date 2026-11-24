package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ReleaseInfo 是一款 CLI 最新版本的更新说明（原文 + 简体中文译文）。
type ReleaseInfo struct {
	ID          string `json:"id"`
	Repo        string `json:"repo,omitempty"`
	Tag         string `json:"tag,omitempty"`
	Version     string `json:"version,omitempty"`
	Title       string `json:"title,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"`
	URL         string `json:"url,omitempty"`
	Body        string `json:"body,omitempty"`
	Zh          string `json:"zh,omitempty"`
	Translated  bool   `json:"translated,omitempty"`
	FetchedAt   int64  `json:"fetchedAt,omitempty"`
	Note        string `json:"note,omitempty"`
	Error       string `json:"error,omitempty"`
}

// ReleaseService 拉取 GitHub 最新发布说明，并把译文缓存在内存和磁盘上。
type ReleaseService struct {
	mu    sync.Mutex
	cache map[string]releaseEntry

	zhMu  sync.Mutex
	zh    map[string]string
	zhSet bool
	path  string

	client *http.Client
}

type releaseEntry struct {
	info    ReleaseInfo
	expires time.Time
}

const (
	releaseCacheTTL   = 30 * time.Minute
	releaseFetchLimit = 6 * time.Second
	translateTimeout  = 20 * time.Second
	translateChunk    = 450  // MyMemory 单次请求上限约 500 字节
	translateMaxBytes = 6000 // 超出的部分不翻译，页面会提示看原文
	translateGap      = 150 * time.Millisecond
)

// NewReleaseService 创建发布说明服务，译持久化在 dataDir/changelog-zh.json。
func NewReleaseService(dataDir string) *ReleaseService {
	return &ReleaseService{
		cache:  map[string]releaseEntry{},
		zh:     map[string]string{},
		path:   filepath.Join(dataDir, "changelog-zh.json"),
		client: &http.Client{Timeout: translateTimeout},
	}
}

// Handler 处理 GET /api/v1/clis/release?id=xxx&refresh=1。
func (s *ReleaseService) Handler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	def, ok := agentByID(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "未知的 CLI"})
		return
	}
	if strings.TrimSpace(def.Repo) == "" {
		writeJSON(w, http.StatusOK, map[string]any{"info": ReleaseInfo{
			ID:    id,
			Error: "暂未收录这款 CLI 的发布源，可在安装目录或官方更新日志里查看",
		}})
		return
	}
	force := r.URL.Query().Get("refresh") == "1"

	s.mu.Lock()
	if !force {
		if entry, ok := s.cache[id]; ok && time.Now().Before(entry.expires) {
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"info": entry.info})
			return
		}
	}
	s.mu.Unlock()

	info := s.load(id, def.Repo)

	s.mu.Lock()
	s.cache[id] = releaseEntry{info: info, expires: time.Now().Add(releaseCacheTTL)}
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"info": info})
}

// load 拉取最新发布并翻译。网络失败时 info.Error 有值，不会 panic。
func (s *ReleaseService) load(id, repo string) ReleaseInfo {
	info := ReleaseInfo{ID: id, Repo: repo, FetchedAt: time.Now().UnixMilli()}
	rel, err := s.latestRelease(repo)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	info.Tag = strings.TrimSpace(rel.TagName)
	info.Version = normalizeVersion(info.Tag)
	info.Title = strings.TrimSpace(rel.Name)
	info.PublishedAt = strings.TrimSpace(rel.PublishedAt)
	info.URL = strings.TrimSpace(rel.HTMLURL)
	info.Body = strings.TrimSpace(rel.Body)
	if info.Body == "" {
		info.Note = "这个版本没有写更新说明，可以直接到发布页看变更记录"
		return info
	}
	if hasCJK(info.Body) {
		info.Zh = info.Body
		info.Translated = true
		return info
	}
	if zh, ok := s.translated(repo, info.Tag, info.Body); ok {
		info.Zh = zh
		info.Translated = true
		return info
	}
	info.Note = "翻译服务暂时不可用，先显示英文原文"
	return info
}

type ghRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	Draft       bool   `json:"draft"`
}

// latestRelease 先取 /releases/latest，说明为空时再翻最近几条找一条有内容的。
func (s *ReleaseService) latestRelease(repo string) (ghRelease, error) {
	var latest ghRelease
	path := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	if err := s.getJSON(path, &latest); err == nil && strings.TrimSpace(latest.Body) != "" {
		return latest, nil
	}
	var list []ghRelease
	path = fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=10", repo)
	if err := s.getJSON(path, &list); err != nil {
		return ghRelease{}, err
	}
	for _, rel := range list {
		if rel.Draft {
			continue
		}
		if strings.TrimSpace(rel.Body) != "" {
			return rel, nil
		}
	}
	if latest.TagName != "" {
		return latest, nil
	}
	if len(list) > 0 {
		return list[0], nil
	}
	return ghRelease{}, errors.New("该仓库没有发布记录")
}

func (s *ReleaseService) getJSON(rawurl string, out any) error {
	req, err := http.NewRequest(http.MethodGet, rawurl, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "agentdeckd/"+version)
	client := &http.Client{Timeout: releaseFetchLimit}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("连不上 GitHub，检查网络后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return errors.New("GitHub 接口限流了，过几分钟再试")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub 返回 %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return errors.New("读取发布说明失败")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errors.New("发布说明格式无法解析")
	}
	return nil
}

// translated 先查译文缓存，再调翻译服务；失败返回 ok=false。
func (s *ReleaseService) translated(repo, tag, body string) (string, bool) {
	key := repo + "@" + tag
	s.zhMu.Lock()
	s.loadZhLocked()
	if zh, ok := s.zh[key]; ok {
		s.zhMu.Unlock()
		return zh, true
	}
	s.zhMu.Unlock()

	text, truncated := clipBody(body, translateMaxBytes)
	zh, err := s.translateText(text)
	if err != nil || strings.TrimSpace(zh) == "" {
		return "", false
	}
	if truncated {
		zh += "\n\n（说明太长，这里只译了前一部分，完整内容见发布页）"
	}

	s.zhMu.Lock()
	s.loadZhLocked()
	s.zh[key] = zh
	if len(s.zh) > 400 {
		for k := range s.zh {
			if k != key {
				delete(s.zh, k)
				break
			}
		}
	}
	raw, _ := json.MarshalIndent(s.zh, "", "  ")
	_ = os.WriteFile(s.path, raw, 0o644)
	s.zhMu.Unlock()
	return zh, true
}

func (s *ReleaseService) loadZhLocked() {
	if s.zhSet {
		return
	}
	s.zhSet = true
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	cached := map[string]string{}
	if json.Unmarshal(raw, &cached) == nil && cached != nil {
		s.zh = cached
	}
}

// translateText 按行切块逐段翻译（每块不超过 450 字节）。
// 免费额度中途用完时保留已译部分，剩下的提示看原文。
func (s *ReleaseService) translateText(text string) (string, error) {
	chunks := chunkRelease(text, translateChunk)
	parts := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		if i > 0 {
			time.Sleep(translateGap)
		}
		out, err := s.myMemory(chunk)
		if err != nil {
			if len(parts) == 0 {
				return "", err
			}
			parts = append(parts, "……（免费翻译额度已用完，后面部分未翻译，可切换「英文原文」查看）")
			return strings.Join(parts, "\n"), nil
		}
		parts = append(parts, out)
	}
	return strings.Join(parts, "\n"), nil
}

func (s *ReleaseService) myMemory(text string) (string, error) {
	rawurl := "https://api.mymemory.translated.net/get?q=" + url.QueryEscape(text) + "&langpair=en%7Czh-CN"
	req, err := http.NewRequest(http.MethodGet, rawurl, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "agentdeckd/"+version)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("翻译服务返回 %d", resp.StatusCode)
	}
	var payload struct {
		ResponseData struct {
			TranslatedText string `json:"translatedText"`
		} `json:"responseData"`
		QuotaFinished  bool `json:"quotaFinished"`
		ResponseStatus any  `json:"responseStatus"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", err
	}
	out := strings.TrimSpace(payload.ResponseData.TranslatedText)
	if payload.QuotaFinished || out == "" || strings.Contains(strings.ToUpper(out), "MYMEMORY WARNING") {
		return "", errors.New("免费翻译额度用完了")
	}
	return out, nil
}

// chunkRelease 按行聚合，单行超长时按句子再切。
func chunkRelease(text string, limit int) []string {
	var chunks []string
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			chunks = append(chunks, strings.TrimRight(buf.String(), "\n"))
			buf.Reset()
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if len(line) > limit {
			flush()
			chunks = append(chunks, splitLong(line, limit)...)
			continue
		}
		if buf.Len() > 0 && buf.Len()+1+len(line) > limit {
			flush()
		}
		if buf.Len() > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(line)
	}
	flush()
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk) != "" {
			out = append(out, chunk)
		}
	}
	return out
}

func splitLong(line string, limit int) []string {
	var out []string
	for len(line) > limit {
		cut := limit
		for cut > 0 && line[cut] != ' ' && line[cut] != '.' {
			cut--
		}
		if cut < limit/2 {
			cut = limit
		}
		out = append(out, strings.TrimSpace(line[:cut]))
		line = strings.TrimSpace(line[cut:])
	}
	if strings.TrimSpace(line) != "" {
		out = append(out, strings.TrimSpace(line))
	}
	return out
}

// clipBody 按字节截断，尽量切在行尾。
func clipBody(body string, limit int) (string, bool) {
	if len(body) <= limit {
		return body, false
	}
	cut := limit
	for cut > 0 && body[cut] != '\n' {
		cut--
	}
	if cut < limit/2 {
		cut = limit
	}
	// 不要切断多字节字符。
	for cut > 0 && !isRuneStart(body[cut]) {
		cut--
	}
	return strings.TrimSpace(body[:cut]), true
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func hasCJK(text string) bool {
	total, cjk := 0, 0
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		total++
		if unicode.Is(unicode.Han, r) {
			cjk++
		}
	}
	return total > 20 && cjk*10 >= total
}

// normalizeVersion 把 v1.2.3 / rust-v0.172.0 / @mastra/core@1.84.0 统一成 1.2.3 形式。
func normalizeVersion(tag string) string {
	tag = strings.TrimSpace(tag)
	if i := strings.LastIndex(tag, "@"); i >= 0 {
		tag = tag[i+1:]
	}
	tag = strings.TrimPrefix(tag, "rust-")
	tag = strings.TrimPrefix(tag, "v")
	return tag
}
