package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	dstpkg "frpconsole/internal/dst"
	"frpconsole/internal/store"
)

type modItem struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	MasterEnabled bool   `json:"masterEnabled"`
	CavesEnabled  bool   `json:"cavesEnabled"`
	InSetup       bool   `json:"inSetup"`
	Downloaded    bool   `json:"downloaded"`
}

type modsPayload struct {
	InstallRoot string    `json:"installRoot,omitempty"`
	SetupPath   string    `json:"setupPath,omitempty"`
	SetupExists bool      `json:"setupExists"`
	SetupError  string    `json:"setupError,omitempty"`
	Shards      []string  `json:"shards"`
	Mods        []modItem `json:"mods"`
	Warnings    []string  `json:"warnings,omitempty"`
}

func (s *Server) handleModsGet(w http.ResponseWriter, r *http.Request) {
	p, err := s.dstInstance(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, s.buildMods(p))
}

func (s *Server) buildMods(p store.DSTProfile) modsPayload {
	payload := modsPayload{Shards: []string{}, Mods: []modItem{}}
	shards, err := dstpkg.DetectShards(p.ClusterDir)
	if err != nil {
		payload.Warnings = append(payload.Warnings, "读取世界列表失败: "+err.Error())
	}

	type shardState struct {
		enabled map[string]bool
	}
	states := map[string]shardState{}
	var order []string
	seen := map[string]bool{}
	for _, sh := range shards {
		payload.Shards = append(payload.Shards, sh.Name)
		path := filepath.Join(sh.Dir, dstpkg.ModOverridesFile)
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				payload.Warnings = append(payload.Warnings, fmt.Sprintf("%s: %v", sh.Name, err))
			}
			continue
		}
		entries, _, _, err := dstpkg.ParseModOverrides(data)
		if err != nil {
			payload.Warnings = append(payload.Warnings, fmt.Sprintf("%s: %v", sh.Name, err))
			continue
		}
		state := shardState{enabled: map[string]bool{}}
		for _, e := range entries {
			state.enabled[e.ID] = e.Enabled
			if !seen[e.ID] {
				seen[e.ID] = true
				order = append(order, e.ID)
			}
		}
		states[sh.Name] = state
	}

	root, _, rootErr := dstpkg.FindServer(s.paths.Settings.SteamCmdDir)
	setupEntries := map[string]string{}
	if rootErr == nil {
		payload.InstallRoot = root
		payload.SetupPath = filepath.Join(root, "mods", dstpkg.ModSetupFile)
		if data, err := os.ReadFile(payload.SetupPath); err == nil {
			payload.SetupExists = true
			setupEntries = dstpkg.SetupEntries(data)
		} else if !os.IsNotExist(err) {
			payload.SetupError = err.Error()
		}
		for id := range setupEntries {
			if !seen[id] {
				seen[id] = true
				order = append(order, id)
			}
		}
	} else {
		payload.SetupError = rootErr.Error()
	}

	sort.Strings(order)
	for _, id := range order {
		item := modItem{ID: id}
		if root != "" {
			item.Name = dstpkg.ModName(root, id)
			item.Downloaded = modDownloaded(root, s.paths.Settings.SteamCmdDir, id)
		}
		if item.Name == "" {
			item.Name = setupEntries[id]
		}
		_, item.InSetup = setupEntries[id]
		if st, ok := states["Master"]; ok {
			item.MasterEnabled = st.enabled[id]
		}
		if st, ok := states["Caves"]; ok {
			item.CavesEnabled = st.enabled[id]
		}
		payload.Mods = append(payload.Mods, item)
	}
	return payload
}

func modDownloaded(installRoot, steamDir, id string) bool {
	dirs := []string{}
	if installRoot != "" {
		dirs = append(dirs, filepath.Join(installRoot, "mods", "workshop-"+id))
	}
	if steamDir != "" {
		dirs = append(dirs, filepath.Join(steamDir, "steamapps", "workshop", "content", "322330", id))
	}
	for _, dir := range dirs {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}

type modUpdateRequest struct {
	Enabled bool     `json:"enabled"`
	Shards  []string `json:"shards"`
}

func (s *Server) handleModsUpdate(w http.ResponseWriter, r *http.Request) {
	p, err := s.dstInstance(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	id := normalizeWorkshopID(r.PathValue("mod"))
	if id == "" {
		fail(w, http.StatusBadRequest, "Workshop ID 无效")
		return
	}
	var req modUpdateRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "invalid body: %v", err)
		return
	}
	shards, err := s.modShards(p, req.Shards)
	if err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	writes := []pendingWrite{}
	for _, sh := range shards {
		path := filepath.Join(sh.Dir, dstpkg.ModOverridesFile)
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			fail(w, http.StatusBadRequest, "%s: %v", sh.Name, err)
			return
		}
		out, err := dstpkg.EnsureMod(data, id, req.Enabled)
		if err != nil {
			fail(w, http.StatusBadRequest, "%s: %v", sh.Name, err)
			return
		}
		writes = append(writes, pendingWrite{path: path, data: out})
	}
	if err := applyWrites(writes); err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, s.buildMods(p))
}

type pendingWrite struct {
	path string
	data []byte
}

func applyWrites(writes []pendingWrite) error {
	for _, wr := range writes {
		if err := dstpkg.WriteFileBackup(wr.path, wr.data); err != nil {
			return fmt.Errorf("%s: %w", wr.path, err)
		}
	}
	return nil
}

func (s *Server) modShards(p store.DSTProfile, wanted []string) ([]dstpkg.Shard, error) {
	shards, err := dstpkg.DetectShards(p.ClusterDir)
	if err != nil {
		return nil, err
	}
	if len(wanted) == 0 {
		return shards, nil
	}
	byName := map[string]dstpkg.Shard{}
	for _, sh := range shards {
		byName[sh.Name] = sh
	}
	var out []dstpkg.Shard
	for _, name := range wanted {
		sh, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("未知分片: %s", name)
		}
		out = append(out, sh)
	}
	return out, nil
}

var modIDFromURL = regexp.MustCompile(`[?&]id=([0-9]+)`)
var modDigits = regexp.MustCompile(`[0-9]{5,}`)

func normalizeWorkshopID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	digitsOnly := true
	for _, r := range raw {
		if r < '0' || r > '9' {
			digitsOnly = false
			break
		}
	}
	if digitsOnly {
		return raw
	}
	if m := modIDFromURL.FindStringSubmatch(raw); m != nil {
		return m[1]
	}
	return modDigits.FindString(raw)
}
