package web

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type modSearchResult struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Preview string `json:"preview,omitempty"`
	URL     string `json:"url"`
}

var (
	workshopAnchorRe = regexp.MustCompile(`(?s)<a[^>]+href="https://steamcommunity\.com/sharedfiles/filedetails/\?id=([0-9]+)[^"]*"[^>]*>(.*?)</a>`)
	workshopTitleRe  = regexp.MustCompile(`(?s)class="workshopItemTitle"[^>]*>(.*?)</div>`)
	workshopImgRe    = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	htmlTagRe        = regexp.MustCompile(`<[^>]+>`)
)

var searchClient = &http.Client{Timeout: 8 * time.Second}

func (s *Server) handleModSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	endpoint := "https://steamcommunity.com/workshop/browse/?appid=322330&section=readytouseitems&browsesort=textsearch&searchtext=" + url.QueryEscape(q)
	if q == "" {
		endpoint = "https://steamcommunity.com/workshop/browse/?appid=322330&section=readytouseitems&browsesort=trend"
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) dstlauncher")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := searchClient.Do(req)
	if err != nil {
		fail(w, http.StatusBadGateway, "访问 Steam 社区失败: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail(w, http.StatusBadGateway, "Steam 社区返回 %s", resp.Status)
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		fail(w, http.StatusBadGateway, "读取 Steam 页面失败: %v", err)
		return
	}
	results := parseWorkshopSearch(string(data))
	writeJSON(w, http.StatusOK, map[string]any{
		"results": results,
		"source":  endpoint,
	})
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
