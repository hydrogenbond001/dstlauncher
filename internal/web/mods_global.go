package web

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	dstpkg "frpconsole/internal/dst"
)

type setupMod struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Downloaded bool   `json:"downloaded"`
}

type setupPayload struct {
	InstallRoot string     `json:"installRoot,omitempty"`
	SetupPath   string     `json:"setupPath,omitempty"`
	SetupExists bool       `json:"setupExists"`
	Mods        []setupMod `json:"mods"`
	Error       string     `json:"error,omitempty"`
}

func (s *Server) setupState() setupPayload {
	payload := setupPayload{Mods: []setupMod{}}
	root, _, err := dstpkg.FindServer(s.paths.Settings.SteamCmdDir)
	if err != nil {
		payload.Error = err.Error()
		return payload
	}
	payload.InstallRoot = root
	payload.SetupPath = filepath.Join(root, "mods", dstpkg.ModSetupFile)
	data, err := os.ReadFile(payload.SetupPath)
	if err != nil && !os.IsNotExist(err) {
		payload.Error = err.Error()
		return payload
	}
	entries := map[string]string{}
	if err == nil {
		payload.SetupExists = true
		entries = dstpkg.SetupEntries(data)
	}
	ids := make([]string, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		name := dstpkg.ModName(root, id)
		if name == "" {
			name = entries[id]
		}
		payload.Mods = append(payload.Mods, setupMod{
			ID:         id,
			Name:       name,
			Downloaded: modDownloaded(root, s.paths.Settings.SteamCmdDir, id),
		})
	}
	return payload
}

func (s *Server) handleSetupGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.setupState())
}

func (s *Server) handleSetupAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkshopID string `json:"workshopID"`
		Name       string `json:"name"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "invalid body: %v", err)
		return
	}
	id := normalizeWorkshopID(req.WorkshopID)
	if id == "" {
		fail(w, http.StatusBadRequest, "Workshop ID 无效")
		return
	}
	root, _, err := dstpkg.FindServer(s.paths.Settings.SteamCmdDir)
	if err != nil {
		fail(w, http.StatusBadRequest, "无法写入下载列表: %v", err)
		return
	}
	path := filepath.Join(root, "mods", dstpkg.ModSetupFile)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		if info, err := s.steamModDetails(id); err == nil {
			name = info.Title
		}
	}
	if err := dstpkg.WriteFileBackup(path, dstpkg.AddSetup(data, id, name)); err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, s.setupState())
}

func (s *Server) handleSetupRemove(w http.ResponseWriter, r *http.Request) {
	id := normalizeWorkshopID(r.PathValue("id"))
	if id == "" {
		fail(w, http.StatusBadRequest, "Workshop ID 无效")
		return
	}
	root, _, err := dstpkg.FindServer(s.paths.Settings.SteamCmdDir)
	if err != nil {
		fail(w, http.StatusBadRequest, "无法写入下载列表: %v", err)
		return
	}
	path := filepath.Join(root, "mods", dstpkg.ModSetupFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, s.setupState())
			return
		}
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	out := dstpkg.RemoveSetup(data, id)
	if string(out) != string(data) {
		if err := dstpkg.WriteFileBackup(path, out); err != nil {
			fail(w, http.StatusInternalServerError, "%v", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, s.setupState())
}
