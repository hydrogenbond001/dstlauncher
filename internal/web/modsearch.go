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
	workshopAnchorRe = regexp.MustCompile(`(?s)<a[^>]+href="https://steamcommunity\.com/sharedfiles/filedetails/\?id=([0-9]+)[^"]*"[^>]*>(.*?)</a>`)
	workshopTitleRe  = regexp.MustCompile(`(?s)class="workshopItemTitle"[^>]*>(.*?)</div>`)
	workshopImgRe    = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	htmlTagRe        = regexp.MustCompile(`<[^>]+>`)
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
	key := strings.TrimSpace(s.paths.Settings.SteamAPIKey)

	var (
		results []modSearchResult
		source  string
		err     error
	)
	if id, ok := numericModID(q); ok {
		var one modSearchResult
		one, err = s.steamModDetails(id)
		if err == nil {
			results = []modSearchResult{one}
			source = "steam-web-api"
		}
	} else if key != "" {
		results, err = s.steamQueryFiles(key, q)
		source = "steam-web-api"
	} else {
		results, err = s.scrapeWorkshop(q)
		source = "steamcommunity"
	}
	if err != nil {
		msg := err.Error()
		if _, isID := numericModID(q); !isID && key == "" {
			msg += "；可在 Mods 窗口配置 Steam API Key"
		}
		fail(w, http.StatusBadGateway, "%s", msg)
		return
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

func (s *Server) steamModDetails(id string) (modSearchResult, error) {
	form := url.Values{}
	form.Set("itemcount", "1")
	form.Set("publishedfileids[0]", id)
	resp, err := s.externalClient(6*time.Second).PostForm("https://api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/", form)
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

func (s *Server) steamQueryFiles(key, q string) ([]modSearchResult, error) {
	u, _ := url.Parse("https://api.steampowered.com/IPublishedFileService/QueryFiles/v1/")
	v := u.Query()
	v.Set("key", key)
	v.Set("appid", "322330")
	v.Set("page", "1")
	v.Set("numperpage", "30")
	v.Set("return_vote_data", "true")
	v.Set("return_children", "true")
	v.Set("requiredtags[0]", "server_only_mod")
	v.Set("requiredtags[1]", "all_clients_require_mod")
	v.Set("match_all_tags", "false")
	v.Set("language", "0")
	if q != "" {
		v.Set("search_text", q)
	} else {
		v.Set("query_type", "1")
	}
	u.RawQuery = v.Encode()
	resp, err := s.externalClient(15 * time.Second).Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("访问 Steam API 失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusForbidden {
		return nil, errors.New("Steam API Key 无效或无权限")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Steam API 返回 %s", resp.Status)
	}
	var out publishedResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("解析 Steam API 响应失败: %w", err)
	}
	results := make([]modSearchResult, 0, len(out.Response.Publishedfiledetails))
	for _, f := range out.Response.Publishedfiledetails {
		if f.ID == "" || f.Result != 1 {
			continue
		}
		results = append(results, publishedToResult(f))
	}
	return results, nil
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

func (s *Server) scrapeWorkshop(q string) ([]modSearchResult, error) {
	endpoint := "https://steamcommunity.com/workshop/browse/?appid=322330&section=readytouseitems&browsesort=textsearch&searchtext=" + url.QueryEscape(q)
	if q == "" {
		endpoint = "https://steamcommunity.com/workshop/browse/?appid=322330&section=readytouseitems&browsesort=trend"
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) dstlauncher")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := s.externalClient(15 * time.Second).Do(req)
	if err != nil {
		return nil, fmt.Errorf("访问 Steam 社区失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Steam 社区返回 %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("读取 Steam 页面失败: %w", err)
	}
	results := parseWorkshopSearch(string(data))
	if len(results) == 0 {
		return results, nil
	}
	return results, nil
}

func parseWorkshopSearch(page string) []modSearchResult {
	var out []modSearchResult
	seen := map[string]bool{}
	for _, m := range workshopAnchorRe.FindAllStringSubmatch(page, -1) {
		id := m[1]
		if seen[id] {
			continue
		}
		titleMatch := workshopTitleRe.FindStringSubmatch(m[2])
		if titleMatch == nil {
			continue
		}
		title := html.UnescapeString(strings.TrimSpace(htmlTagRe.ReplaceAllString(titleMatch[1], "")))
		if title == "" {
			continue
		}
		preview := ""
		if img := workshopImgRe.FindStringSubmatch(m[2]); img != nil {
			preview = html.UnescapeString(img[1])
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
