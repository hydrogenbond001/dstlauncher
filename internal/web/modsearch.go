package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type modSearchResult struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Preview       string `json:"preview,omitempty"`
	URL           string `json:"url"`
	Size          int64  `json:"size,omitempty"`
	Subscriptions int    `json:"subscriptions,omitempty"`
	Updated       int64  `json:"updated,omitempty"`
}

var (
	workshopAnchorRe = regexp.MustCompile(`(?s)<a[^>]+href="https?://steamcommunity\.com/sharedfiles/filedetails/\?id=([0-9]+)[^"]*"[^>]*>(.*?)</a>`)
	workshopTitleRe  = regexp.MustCompile(`(?s)class="workshopItemTitle"[^>]*>(.*?)</div>`)
	workshopImgRe    = regexp.MustCompile(`<img[^>]*>`)
	workshopAltRe    = regexp.MustCompile(`alt="([^"]*)"`)
	workshopSrcRe    = regexp.MustCompile(`src="([^"]+)"`)
	htmlTagRe        = regexp.MustCompile(`<[^>]+>`)
)

// workshopMirrors 是 Steam 社区的官方镜像域名。主域 steamcommunity.com 在部分网络被
// DNS 污染而无法访问，镜像提供同样的工坊搜索页，且不需要登录或 API Key。
var workshopMirrors = []string{
	"https://community.steamstatic.com",
	"https://community.akamai.steamstatic.com",
	"https://steamcommunity.com",
}

var (
	mirrorMu     sync.Mutex
	mirrorPick   = 0 // 最近一次可用镜像的下标，优先复用
	searchClient = &http.Client{Timeout: 15 * time.Second}
	detailClient = &http.Client{Timeout: 6 * time.Second}
	batchClient  = &http.Client{Timeout: 7 * time.Second}
)

type publishedFile struct {
	ID            string `json:"publishedfileid"`
	Result        int    `json:"result"`
	Title         string `json:"title"`
	Preview       string `json:"preview_url"`
	FileSize      string `json:"file_size"`
	TimeUpdated   int64  `json:"time_updated"`
	Subscriptions int    `json:"subscriptions"`
}

type publishedResponse struct {
	Response struct {
		Publishedfiledetails []publishedFile `json:"publishedfiledetails"`
	} `json:"response"`
}

func (s *Server) handleModSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	var (
		results []modSearchResult
		source  string
		err     error
	)
	if id, ok := numericModID(q); ok {
		var one modSearchResult
		one, err = steamModDetails(id)
		if err == nil {
			results = []modSearchResult{one}
			source = "steam-web-api"
		}
	} else {
		results, source, err = browseWorkshop(q)
		if err == nil && len(results) > 0 {
			enrichModResults(results)
		}
	}
	if err != nil {
		fail(w, http.StatusBadGateway, "%s", err.Error())
		return
	}
	if results == nil {
		results = []modSearchResult{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results": results,
		"source":  source,
	})
}

func numericModID(q string) (string, bool) {
	if len(q) < 5 {
		return "", false
	}
	for _, r := range q {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return q, true
}

func steamModDetails(id string) (modSearchResult, error) {
	form := url.Values{}
	form.Set("itemcount", "1")
	form.Set("publishedfileids[0]", id)
	resp, err := detailClient.PostForm("https://api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/", form)
	if err != nil {
		return modSearchResult{}, fmt.Errorf("访问 Steam API 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return modSearchResult{}, fmt.Errorf("Steam API 返回 %s", resp.Status)
	}
	var out publishedResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return modSearchResult{}, fmt.Errorf("解析 Steam API 响应失败: %w", err)
	}
	if len(out.Response.Publishedfiledetails) == 0 {
		return modSearchResult{}, fmt.Errorf("Steam 未找到 Mod %s", id)
	}
	f := out.Response.Publishedfiledetails[0]
	if f.Result != 1 || f.ID == "" {
		return modSearchResult{}, fmt.Errorf("Steam 未找到 Mod %s", id)
	}
	return publishedToResult(f), nil
}

// steamBatchDetails 通过公开的 GetPublishedFileDetails 批量取回 Mod 元数据；无需 API Key。
// 失败时返回 nil，调用方保留网页解析到的信息。
func steamBatchDetails(ids []string) map[string]publishedFile {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > 30 {
		ids = ids[:30]
	}
	form := url.Values{}
	form.Set("itemcount", strconv.Itoa(len(ids)))
	for i, id := range ids {
		form.Set(fmt.Sprintf("publishedfileids[%d]", i), id)
	}
	resp, err := batchClient.PostForm("https://api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/", form)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var out publishedResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&out); err != nil {
		return nil
	}
	byID := make(map[string]publishedFile, len(out.Response.Publishedfiledetails))
	for _, f := range out.Response.Publishedfiledetails {
		if f.ID != "" && f.Result == 1 {
			byID[f.ID] = f
		}
	}
	return byID
}

// enrichModResults 补齐规范标题、预览图、大小、订阅数与更新时间。
// Steam 对该接口有限流且国内延迟不稳，因此分片并发请求，整体限时，
// 拿不到就保留网页解析结果，不阻塞搜索。
func enrichModResults(results []modSearchResult) {
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.ID)
	}
	byID := steamBatchDetailsParallel(ids, enrichChunk)
	if len(byID) == 0 {
		return
	}
	for i := range results {
		f, ok := byID[results[i].ID]
		if !ok {
			continue
		}
		if title := strings.TrimSpace(f.Title); title != "" {
			results[i].Title = title
		}
		if preview := strings.TrimSpace(f.Preview); preview != "" {
			results[i].Preview = preview
		}
		if results[i].URL == "" {
			results[i].URL = "https://steamcommunity.com/sharedfiles/filedetails/?id=" + results[i].ID
		}
		size, _ := strconv.ParseInt(f.FileSize, 10, 64)
		results[i].Size = size
		results[i].Subscriptions = f.Subscriptions
		results[i].Updated = f.TimeUpdated
	}
}

const enrichChunk = 6

func steamBatchDetailsParallel(ids []string, size int) map[string]publishedFile {
	if size < 1 {
		size = enrichChunk
	}
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		merged = map[string]publishedFile{}
	)
	for i := 0; i < len(ids); i += size {
		end := i + size
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[i:end]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id, f := range steamBatchDetails(chunk) {
				mu.Lock()
				merged[id] = f
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return merged
}

func publishedToResult(f publishedFile) modSearchResult {
	id := f.ID
	title := strings.TrimSpace(f.Title)
	if title == "" {
		title = "workshop-" + id
	}
	size, _ := strconv.ParseInt(f.FileSize, 10, 64)
	return modSearchResult{
		ID:            id,
		Title:         title,
		Preview:       f.Preview,
		URL:           "https://steamcommunity.com/sharedfiles/filedetails/?id=" + id,
		Size:          size,
		Subscriptions: f.Subscriptions,
		Updated:       f.TimeUpdated,
	}
}

// browseWorkshop 通过 Steam 社区镜像搜索工坊（支持中文关键词，无需 API Key）。
// 依次尝试镜像，复用最近可用的那个。
func browseWorkshop(q string) ([]modSearchResult, string, error) {
	order := mirrorOrder()
	var lastErr error
	for _, idx := range order {
		host := workshopMirrors[idx]
		results, err := fetchWorkshopSearch(host, q)
		if err != nil {
			lastErr = err
			continue
		}
		rememberMirror(idx)
		return results, "steam-workshop", nil
	}
	if lastErr == nil {
		lastErr = errors.New("无法访问 Steam 工坊")
	}
	return nil, "", fmt.Errorf("%w（已尝试 %d 个 Steam 站点）", lastErr, len(order))
}

func mirrorOrder() []int {
	mirrorMu.Lock()
	pick := mirrorPick
	mirrorMu.Unlock()
	if pick < 0 || pick >= len(workshopMirrors) {
		pick = 0
	}
	out := make([]int, 0, len(workshopMirrors))
	for i := range workshopMirrors {
		out = append(out, (pick+i)%len(workshopMirrors))
	}
	return out
}

func rememberMirror(idx int) {
	mirrorMu.Lock()
	mirrorPick = idx
	mirrorMu.Unlock()
}

func fetchWorkshopSearch(host, q string) ([]modSearchResult, error) {
	endpoint := host + "/workshop/browse/?appid=322330&l=chinese&section=readytouseitems"
	if q != "" {
		endpoint += "&browsesort=textsearch&searchtext=" + url.QueryEscape(q)
	} else {
		endpoint += "&browsesort=trend"
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) dstlauncher")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	resp, err := searchClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("访问 Steam 社区失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Steam 社区返回 %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("读取 Steam 页面失败: %w", err)
	}
	return parseWorkshopSearch(string(data)), nil
}

func parseWorkshopSearch(page string) []modSearchResult {
	var out []modSearchResult
	seen := map[string]bool{}
	for _, m := range workshopAnchorRe.FindAllStringSubmatch(page, -1) {
		id := m[1]
		if seen[id] {
			continue
		}
		title, preview := workshopItemInfo(m[2])
		if title == "" {
			continue
		}
		seen[id] = true
		out = append(out, modSearchResult{
			ID:      id,
			Title:   title,
			Preview: preview,
			URL:     fmt.Sprintf("https://steamcommunity.com/sharedfiles/filedetails/?id=%s", id),
		})
		if len(out) >= 30 {
			break
		}
	}
	return out
}

// workshopItemInfo 兼容工坊列表的两种结构：旧版标题在 div.workshopItemTitle，
// 新版（React）标题写在缩略图的 alt 属性上。
func workshopItemInfo(inner string) (string, string) {
	title, preview := "", ""
	if img := workshopImgRe.FindString(inner); img != "" {
		if a := workshopAltRe.FindStringSubmatch(img); a != nil {
			title = cleanWorkshopText(a[1])
		}
		if s := workshopSrcRe.FindStringSubmatch(img); s != nil {
			preview = strings.TrimSpace(html.UnescapeString(s[1]))
		}
	}
	if t := workshopTitleRe.FindStringSubmatch(inner); t != nil {
		if cleaned := cleanWorkshopText(t[1]); cleaned != "" {
			title = cleaned
		}
	}
	return title, preview
}

func cleanWorkshopText(raw string) string {
	text := htmlTagRe.ReplaceAllString(raw, " ")
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, "\u00a0", " ")
	return strings.TrimSpace(text)
}
