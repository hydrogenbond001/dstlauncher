// Package web serves the local panel UI and its JSON API.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"frpconsole/internal/app"
	dstpkg "frpconsole/internal/dst"
	"frpconsole/internal/frpx"
	"frpconsole/internal/store"
	"frpconsole/internal/unit"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

type steamJob struct {
	mu       sync.Mutex
	running  bool
	success  bool
	errMsg   string
	started  time.Time
	finished time.Time
	lines    []string
	logFile  *os.File
	pending  string
	cancel   context.CancelFunc
}

func (j *steamJob) addLocked(line string) {
	j.lines = append(j.lines, line)
	if len(j.lines) > 300 {
		j.lines = j.lines[len(j.lines)-300:]
	}
	if j.logFile != nil {
		_, _ = j.logFile.WriteString(line + "\n")
	}
}

func (j *steamJob) finish(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.pending != "" {
		j.addLocked(j.pending)
		j.pending = ""
	}
	j.running = false
	j.finished = time.Now()
	if j.logFile != nil {
		_ = j.logFile.Close()
		j.logFile = nil
	}
	if err != nil {
		j.errMsg = err.Error()
		return
	}
	for _, line := range j.lines {
		if strings.Contains(line, "Success!") {
			j.success = true
			return
		}
	}
	j.errMsg = "命令结束，但没有看到 Success!，请查看日志"
}

func (j *steamJob) reset() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.running {
		return errors.New("steamcmd 正在更新")
	}
	j.success = false
	j.errMsg = ""
	j.started = time.Time{}
	j.finished = time.Time{}
	j.lines = nil
	j.pending = ""
	return nil
}

func (j *steamJob) snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := map[string]any{
		"running": j.running,
		"success": j.success,
		"error":   j.errMsg,
	}
	if !j.started.IsZero() {
		out["startedAt"] = j.started.Format(time.RFC3339)
		out["elapsed"] = int(time.Since(j.started).Seconds())
	}
	if !j.finished.IsZero() {
		out["finishedAt"] = j.finished.Format(time.RFC3339)
	}
	start := 0
	if len(j.lines) > 60 {
		start = len(j.lines) - 60
	}
	out["lines"] = append([]string(nil), j.lines[start:]...)
	return out
}

type steamWriter struct{ job *steamJob }

func (w steamWriter) Write(p []byte) (int, error) {
	j := w.job
	j.mu.Lock()
	defer j.mu.Unlock()
	j.pending += string(p)
	for {
		i := strings.IndexByte(j.pending, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(j.pending[:i], "\r")
		j.pending = j.pending[i+1:]
		j.addLocked(line)
	}
	return len(p), nil
}

type Server struct {
	paths    *app.Paths
	store    *store.Store
	dstStore *store.DSTStore
	assets   fs.FS
	version  string

	mu    sync.Mutex
	procs map[string]*unit.Proc
	steam *steamJob
	quit  func()
}

// SetQuit wires the panel's shutdown hook (tray/HTTP quit).
func (s *Server) SetQuit(fn func()) { s.quit = fn }

func New(p *app.Paths, st *store.Store, dstStore *store.DSTStore, assets fs.FS, version string) *Server {
	return &Server{
		paths:    p,
		store:    st,
		dstStore: dstStore,
		assets:   assets,
		version:  version,
		procs:    make(map[string]*unit.Proc),
		steam:    &steamJob{},
	}
}

func (s *Server) Handler() http.Handler {
	sub, err := fs.Sub(s.assets, "web")
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(sub)))
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/quit", s.handleQuit)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/profiles", s.handleCreate)
	mux.HandleFunc("DELETE /api/profiles/{id}", s.handleDelete)
	mux.HandleFunc("GET /api/profiles/{id}/config", s.handleConfigGet)
	mux.HandleFunc("PUT /api/profiles/{id}/config", s.handleConfigPut)
	mux.HandleFunc("POST /api/profiles/{id}/start", s.handleStart)
	mux.HandleFunc("POST /api/profiles/{id}/stop", s.handleStop)
	mux.HandleFunc("POST /api/profiles/{id}/restart", s.handleRestart)
	mux.HandleFunc("GET /api/profiles/{id}/logs", s.handleLogs)
	mux.HandleFunc("GET /api/profiles/{id}/status", s.handleStatus)
	mux.HandleFunc("POST /api/dst", s.handleDSTCreate)
	mux.HandleFunc("DELETE /api/dst/{id}", s.handleDSTDelete)
	mux.HandleFunc("POST /api/dst/{id}/start", s.handleDSTStart)
	mux.HandleFunc("POST /api/dst/{id}/stop", s.handleDSTStop)
	mux.HandleFunc("POST /api/dst/{id}/restart", s.handleDSTRestart)
	mux.HandleFunc("GET /api/dst/{id}/logs", s.handleDSTLogs)
	mux.HandleFunc("GET /api/dst/{id}/status", s.handleDSTStatus)
	mux.HandleFunc("GET /api/dst/{id}/config", s.handleDSTConfigGet)
	mux.HandleFunc("PUT /api/dst/{id}/config", s.handleDSTConfigPut)
	mux.HandleFunc("GET /api/settings", s.handleSettingsGet)
	mux.HandleFunc("PUT /api/settings", s.handleSettingsPut)
	mux.HandleFunc("GET /api/steamcmd/status", s.handleSteamStatus)
	mux.HandleFunc("POST /api/steamcmd/update", s.handleSteamUpdate)
	mux.HandleFunc("POST /api/steamcmd/test", s.handleSteamCmdTest)
	mux.HandleFunc("POST /api/steamcmd/clear", s.handleSteamClear)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"app": "frp-console", "version": s.version})
}

func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	if s.quit != nil {
		go func() {
			time.Sleep(150 * time.Millisecond) // let the response flush
			s.quit()
		}()
	}
}

type profileView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	CreatedAt  string `json:"createdAt"`
	Running    bool   `json:"running"`
	PID        int    `json:"pid,omitempty"`
	ConfigPath string `json:"configPath"`
	WebPort    int    `json:"webPort,omitempty"`
	WebURL     string `json:"webUrl,omitempty"`
	BinPath    string `json:"binPath,omitempty"`
	BinError   string `json:"binError,omitempty"`
}

func (s *Server) view(p store.Profile) profileView {
	v := profileView{
		ID:         p.ID,
		Name:       p.Name,
		Kind:       p.Kind,
		CreatedAt:  p.CreatedAt.Format(time.RFC3339),
		ConfigPath: filepath.Join(s.store.Dir(p.ID), frpx.ConfigName(p.Kind)),
	}
	if data, err := os.ReadFile(v.ConfigPath); err == nil {
		if port, _, _ := frpx.WebAuth(data); port > 0 {
			v.WebPort = port
			v.WebURL = fmt.Sprintf("http://127.0.0.1:%d/static/", port)
		}
	}
	if bin, err := s.paths.FindBin(binName(p.Kind)); err == nil {
		v.BinPath = bin
	} else {
		v.BinError = err.Error()
	}
	st := s.frpProc(p.ID).Status()
	v.Running = st.Running
	v.PID = st.PID
	return v
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	bin := map[string]string{"frpc": "", "frps": ""}
	for _, name := range []string{"frpc", "frps"} {
		if path, err := s.paths.FindBin(name); err == nil {
			bin[name] = path
		}
	}
	list := s.store.List()
	views := make([]profileView, 0, len(list))
	for _, p := range list {
		views = append(views, s.view(p))
	}
	dstList := s.dstStore.List()
	dstViews := make([]dstView, 0, len(dstList))
	for _, p := range dstList {
		dstViews = append(dstViews, s.dstView(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":     s.version,
		"root":        s.paths.Root,
		"listen":      s.paths.Settings.Listen,
		"bin":         bin,
		"steamcmdDir": s.paths.Settings.SteamCmdDir,
		"profiles":    views,
		"dst":         dstViews,
	})
}

type createRequest struct {
	Name   string           `json:"name"`
	Kind   string           `json:"kind"`
	Client *frpx.ClientSpec `json:"client,omitempty"`
	Server *frpx.ServerSpec `json:"server,omitempty"`
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "invalid body: %v", err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		fail(w, http.StatusBadRequest, "name is required")
		return
	}

	var (
		cfg  []byte
		kind string
	)
	switch req.Kind {
	case "client":
		if req.Client == nil {
			fail(w, http.StatusBadRequest, "client spec is required")
			return
		}
		if err := req.Client.Validate(); err != nil {
			fail(w, http.StatusBadRequest, "%v", err)
			return
		}
		if req.Client.WebPort == 0 {
			req.Client.WebPort = 7400
		}
		if req.Client.WebUser == "" {
			req.Client.WebUser = "admin"
		}
		cfg = frpx.RenderClient(*req.Client)
		kind = "client"
	case "server":
		if req.Server == nil {
			fail(w, http.StatusBadRequest, "server spec is required")
			return
		}
		if err := req.Server.Validate(); err != nil {
			fail(w, http.StatusBadRequest, "%v", err)
			return
		}
		if req.Server.WebPort == 0 {
			req.Server.WebPort = 7500
		}
		if req.Server.WebUser == "" {
			req.Server.WebUser = "admin"
		}
		cfg = frpx.RenderServer(*req.Server)
		kind = "server"
	default:
		fail(w, http.StatusBadRequest, "kind must be client or server")
		return
	}

	bin, err := s.paths.FindBin(binName(kind))
	if err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}

	id := s.store.UniqueID(req.Name)
	dir := s.store.Dir(id)
	cfgPath := filepath.Join(dir, frpx.ConfigName(kind))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(w, http.StatusInternalServerError, "create profile dir: %v", err)
		return
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := app.WriteAtomic(cfgPath, cfg, 0o644); err != nil {
		cleanup()
		fail(w, http.StatusInternalServerError, "write config: %v", err)
		return
	}
	if err := frpx.Verify(bin, cfgPath); err != nil {
		cleanup()
		fail(w, http.StatusBadRequest, "config rejected by %s: %v", filepath.Base(bin), err)
		return
	}

	p := store.Profile{ID: id, Name: req.Name, Kind: kind, CreatedAt: time.Now()}
	if err := s.store.Add(p); err != nil {
		cleanup()
		fail(w, http.StatusInternalServerError, "save profile: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, s.view(p))
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	prof, err := s.profile(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	s.frpProc(prof.ID).Stop(2 * time.Second)
	s.mu.Lock()
	delete(s.procs, prof.ID)
	s.mu.Unlock()
	if err := s.store.Delete(prof.ID); err != nil {
		fail(w, http.StatusInternalServerError, "delete profile: %v", err)
		return
	}
	_ = os.Remove(filepath.Join(s.paths.Logs, prof.ID+".log"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	prof, err := s.profile(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	path := filepath.Join(s.store.Dir(prof.ID), frpx.ConfigName(prof.Kind))
	data, err := os.ReadFile(path)
	if err != nil {
		fail(w, http.StatusInternalServerError, "read config: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path, "content": string(data)})
}

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	prof, err := s.profile(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		fail(w, http.StatusBadRequest, "read body: %v", err)
		return
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		fail(w, http.StatusBadRequest, "config cannot be empty")
		return
	}
	bin, err := s.paths.FindBin(binName(prof.Kind))
	if err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}

	path := filepath.Join(s.store.Dir(prof.ID), frpx.ConfigName(prof.Kind))
	oldPort, oldUser, oldPass := 0, "", ""
	if old, err := os.ReadFile(path); err == nil {
		oldPort, oldUser, oldPass = frpx.WebAuth(old)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(path), ".verify-*.tmp"+filepath.Ext(path))
	if err != nil {
		fail(w, http.StatusInternalServerError, "temp file: %v", err)
		return
	}
	tmp := tmpFile.Name()
	defer os.Remove(tmp)
	if _, err := tmpFile.Write(body); err != nil {
		tmpFile.Close()
		fail(w, http.StatusInternalServerError, "write config: %v", err)
		return
	}
	if err := tmpFile.Close(); err != nil {
		fail(w, http.StatusInternalServerError, "write config: %v", err)
		return
	}
	if err := frpx.Verify(bin, tmp); err != nil {
		fail(w, http.StatusBadRequest, "config rejected by %s: %v", filepath.Base(bin), err)
		return
	}

	if err := app.WriteAtomic(path, body, 0o644); err != nil {
		fail(w, http.StatusInternalServerError, "write config: %v", err)
		return
	}

	reloadErr := ""
	if s.frpProc(prof.ID).Running() && oldPort > 0 && prof.Kind == "client" {
		if _, err := frpx.Admin(oldPort, oldUser, oldPass, http.MethodGet, "/api/reload", nil); err != nil {
			reloadErr = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "reloadError": reloadErr})
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	prof, err := s.profile(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	bin, err := s.paths.FindBin(binName(prof.Kind))
	if err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	dir := s.store.Dir(prof.ID)
	cfgPath := filepath.Join(dir, frpx.ConfigName(prof.Kind))
	if _, err := os.Stat(cfgPath); err != nil {
		fail(w, http.StatusBadRequest, "config missing: %v", err)
		return
	}
	proc := s.frpProc(prof.ID)
	if proc.Running() {
		fail(w, http.StatusConflict, "already running")
		return
	}
	if err := proc.Start(bin, []string{"-c", cfgPath}, dir); err != nil {
		fail(w, http.StatusInternalServerError, "start: %v", err)
		return
	}
	time.Sleep(300 * time.Millisecond)
	writeJSON(w, http.StatusOK, s.view(prof))
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	prof, err := s.profile(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	s.stopProfile(prof)
	writeJSON(w, http.StatusOK, s.view(prof))
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	prof, err := s.profile(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	bin, err := s.paths.FindBin(binName(prof.Kind))
	if err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	s.stopProfile(prof)
	dir := s.store.Dir(prof.ID)
	cfgPath := filepath.Join(dir, frpx.ConfigName(prof.Kind))
	if err := s.frpProc(prof.ID).Start(bin, []string{"-c", cfgPath}, dir); err != nil {
		fail(w, http.StatusInternalServerError, "start: %v", err)
		return
	}
	time.Sleep(300 * time.Millisecond)
	writeJSON(w, http.StatusOK, s.view(prof))
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	prof, err := s.profile(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	s.writeLog(w, filepath.Join(s.paths.Logs, prof.ID+".log"), offset)
}

func (s *Server) writeLog(w http.ResponseWriter, path string, offset int) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fail(w, http.StatusInternalServerError, "read log: %v", err)
		return
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = ansiRE.ReplaceAllString(text, "")
	text = strings.TrimRight(text, "\n")
	var lines []string
	if text != "" {
		lines = strings.Split(text, "\n")
	}
	const maxLines = 5000
	base := 0
	if len(lines) > maxLines {
		base = len(lines) - maxLines
		lines = lines[base:]
	}
	if offset < base {
		offset = base
	}
	start := offset - base
	if start > len(lines) {
		start = len(lines)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lines": lines[start:],
		"next":  base + len(lines),
		"reset": offset < base,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	prof, err := s.profile(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	view := s.view(prof)
	st := s.frpProc(prof.ID).Status()
	resp := map[string]any{
		"running":    st.Running,
		"pid":        st.PID,
		"startedAt":  st.StartedAt,
		"exitAt":     st.ExitAt,
		"lastError":  st.LastError,
		"configPath": view.ConfigPath,
		"logPath":    filepath.Join(s.paths.Logs, prof.ID+".log"),
		"webUrl":     view.WebURL,
	}
	if !st.Running {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	cfg, err := os.ReadFile(view.ConfigPath)
	if err != nil {
		resp["adminError"] = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	port, user, pass := frpx.WebAuth(cfg)
	if port == 0 {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	path := "/api/status"
	if prof.Kind == "server" {
		path = "/api/v2/proxies"
	}
	body, err := frpx.Admin(port, user, pass, http.MethodGet, path, nil)
	if err != nil {
		resp["adminError"] = err.Error()
	} else {
		var v any
		if err := json.Unmarshal(body, &v); err == nil {
			resp["admin"] = v
		} else {
			resp["admin"] = string(body)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) stopProfile(prof store.Profile) {
	proc := s.frpProc(prof.ID)
	if !proc.Running() {
		return
	}
	if prof.Kind == "client" {
		path := filepath.Join(s.store.Dir(prof.ID), frpx.ConfigName(prof.Kind))
		if cfg, err := os.ReadFile(path); err == nil {
			if port, user, pass := frpx.WebAuth(cfg); port > 0 {
				_, _ = frpx.Admin(port, user, pass, http.MethodPost, "/api/stop", nil)
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) && proc.Running() {
					time.Sleep(50 * time.Millisecond)
				}
			}
		}
	}
	_ = proc.Stop(3 * time.Second)
}

func (s *Server) StopAll() {
	if s.steam != nil {
		s.steam.mu.Lock()
		if s.steam.running && s.steam.cancel != nil {
			s.steam.cancel()
		}
		s.steam.mu.Unlock()
	}
	s.mu.Lock()
	procs := make([]*unit.Proc, 0, len(s.procs))
	for _, p := range s.procs {
		procs = append(procs, p)
	}
	s.mu.Unlock()
	for _, p := range procs {
		_ = p.Stop(2 * time.Second)
	}
}

func (s *Server) profile(r *http.Request) (store.Profile, error) {
	id := r.PathValue("id")
	p, ok := s.store.Get(id)
	if !ok {
		return store.Profile{}, fmt.Errorf("profile %q not found", id)
	}
	return p, nil
}

func (s *Server) proc(key, logName string) *unit.Proc {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.procs[key]
	if !ok {
		p = unit.New(filepath.Join(s.paths.Logs, logName+".log"))
		s.procs[key] = p
	}
	return p
}

func (s *Server) frpProc(id string) *unit.Proc { return s.proc(id, id) }

func (s *Server) dstProc(id string) *unit.Proc { return s.proc("dst:"+id, "dst-"+id) }

type dstShardView struct {
	Name      string `json:"name"`
	Dir       string `json:"dir"`
	IsMaster  bool   `json:"isMaster"`
	Port      int    `json:"port"`
	Running   bool   `json:"running"`
	PID       int    `json:"pid,omitempty"`
	ExitAt    string `json:"exitAt,omitempty"`
	LastError string `json:"lastError,omitempty"`
}

type dstView struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	ClusterDir   string         `json:"clusterDir"`
	Command      string         `json:"command"`
	CreatedAt    string         `json:"createdAt"`
	Shards       []dstShardView `json:"shards"`
	Running      bool           `json:"running"`
	PID          int            `json:"pid,omitempty"`
	ClusterDirOK bool           `json:"clusterDirOk"`
	LogPath      string         `json:"logPath"`
}

func (s *Server) dstShardProc(id, shard string) *unit.Proc {
	return s.proc("dst:"+id+":"+shard, "dst-"+id)
}

func (s *Server) dstView(p store.DSTProfile) dstView {
	v := dstView{
		ID:         p.ID,
		Name:       p.Name,
		ClusterDir: p.ClusterDir,
		Command:    s.launchCommand(p),
		CreatedAt:  p.CreatedAt.Format(time.RFC3339),
		LogPath:    filepath.Join(s.paths.Logs, "dst-"+p.ID+".log"),
	}
	if info, err := os.Stat(p.ClusterDir); err == nil && info.IsDir() {
		v.ClusterDirOK = true
	}
	shards, _ := dstpkg.DetectShards(p.ClusterDir)
	for _, sh := range shards {
		st := s.dstShardProc(p.ID, sh.Name).Status()
		v.Shards = append(v.Shards, dstShardView{
			Name: sh.Name, Dir: sh.Dir, IsMaster: sh.IsMaster, Port: sh.Port,
			Running: st.Running, PID: st.PID, ExitAt: st.ExitAt, LastError: st.LastError,
		})
		if st.Running {
			v.Running = true
			if v.PID == 0 {
				v.PID = st.PID
			}
		}
	}
	return v
}

func (s *Server) launchCommand(p store.DSTProfile) string {
	args := p.LaunchArgs
	if len(args) == 0 {
		args = dstpkg.LaunchArgs(p.ClusterDir, "")
	}
	exe := "<server exe>"
	if _, path, err := dstpkg.FindServer(s.paths.Settings.SteamCmdDir); err == nil {
		exe = path
	}
	parts := append([]string{exe}, args...)
	return strings.Join(parts, " ") + " -shard <Master|Caves>"
}

type dstCreateRequest struct {
	Name       string `json:"name"`
	ClusterDir string `json:"clusterDir"`
}

func (s *Server) handleDSTCreate(w http.ResponseWriter, r *http.Request) {
	var req dstCreateRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "invalid body: %v", err)
		return
	}
	p := store.DSTProfile{
		Name:       strings.TrimSpace(req.Name),
		ClusterDir: strings.TrimSpace(req.ClusterDir),
	}
	if p.Name == "" {
		fail(w, http.StatusBadRequest, "名称不能为空")
		return
	}
	if err := dstpkg.ValidateClusterDir(p.ClusterDir); err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	p.LaunchArgs = dstpkg.LaunchArgs(p.ClusterDir, "")
	p.ID = s.dstStore.UniqueID(p.Name)
	p.CreatedAt = time.Now()
	if err := s.dstStore.Add(p); err != nil {
		fail(w, http.StatusInternalServerError, "save instance: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, s.dstView(p))
}

func (s *Server) handleDSTDelete(w http.ResponseWriter, r *http.Request) {
	p, err := s.dstInstance(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	s.stopDST(p)
	s.mu.Lock()
	for key := range s.procs {
		if key == "dst:"+p.ID || strings.HasPrefix(key, "dst:"+p.ID+":") {
			delete(s.procs, key)
		}
	}
	s.mu.Unlock()
	if err := s.dstStore.Delete(p.ID); err != nil {
		fail(w, http.StatusInternalServerError, "delete: %v", err)
		return
	}
	_ = os.Remove(filepath.Join(s.paths.Logs, "dst-"+p.ID+".log"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDSTConfigGet(w http.ResponseWriter, r *http.Request) {
	p, err := s.dstInstance(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	view, err := dstpkg.ReadConfig(p.ClusterDir)
	if err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleDSTConfigPut(w http.ResponseWriter, r *http.Request) {
	p, err := s.dstInstance(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	var view dstpkg.ConfigView
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&view); err != nil {
		fail(w, http.StatusBadRequest, "invalid body: %v", err)
		return
	}
	view.ClusterDir = p.ClusterDir
	if err := dstpkg.WriteConfig(view); err != nil {
		fail(w, http.StatusBadRequest, "写入配置失败: %v", err)
		return
	}
	fresh, err := dstpkg.ReadConfig(p.ClusterDir)
	if err != nil {
		fail(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, fresh)
}

func (s *Server) handleDSTStart(w http.ResponseWriter, r *http.Request) {
	p, err := s.dstInstance(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	if err := s.startDST(p); err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	time.Sleep(400 * time.Millisecond)
	writeJSON(w, http.StatusOK, s.dstView(p))
}

func (s *Server) handleDSTStop(w http.ResponseWriter, r *http.Request) {
	p, err := s.dstInstance(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	s.stopDST(p)
	writeJSON(w, http.StatusOK, s.dstView(p))
}

func (s *Server) handleDSTRestart(w http.ResponseWriter, r *http.Request) {
	p, err := s.dstInstance(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	s.stopDST(p)
	if err := s.startDST(p); err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	time.Sleep(400 * time.Millisecond)
	writeJSON(w, http.StatusOK, s.dstView(p))
}

func (s *Server) handleDSTLogs(w http.ResponseWriter, r *http.Request) {
	p, err := s.dstInstance(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	path := filepath.Join(s.paths.Logs, "dst-"+p.ID+".log")
	source := r.URL.Query().Get("source")
	if source != "" && source != "launcher" {
		shards, _ := dstpkg.DetectShards(p.ClusterDir)
		found := false
		for _, sh := range shards {
			if sh.Name == source {
				path = filepath.Join(sh.Dir, "server_log.txt")
				found = true
				break
			}
		}
		if !found {
			fail(w, http.StatusBadRequest, "未知日志来源: %s", source)
			return
		}
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	s.writeLog(w, path, offset)
}

func (s *Server) handleDSTStatus(w http.ResponseWriter, r *http.Request) {
	p, err := s.dstInstance(r)
	if err != nil {
		fail(w, http.StatusNotFound, "%v", err)
		return
	}
	v := s.dstView(p)
	resp := map[string]any{
		"running":      v.Running,
		"pid":          v.PID,
		"clusterDir":   p.ClusterDir,
		"command":      v.Command,
		"clusterDirOk": v.ClusterDirOK,
		"logPath":      v.LogPath,
		"shards":       v.Shards,
	}
	if v.Running {
		for _, sh := range v.Shards {
			if sh.Running {
				st := s.dstShardProc(p.ID, sh.Name).Status()
				resp["startedAt"] = st.StartedAt
				resp["lastError"] = st.LastError
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) dstInstance(r *http.Request) (store.DSTProfile, error) {
	id := r.PathValue("id")
	p, ok := s.dstStore.Get(id)
	if !ok {
		return store.DSTProfile{}, fmt.Errorf("DST 实例 %q 不存在", id)
	}
	return p, nil
}

func (s *Server) startDST(p store.DSTProfile) error {
	s.steam.mu.Lock()
	updating := s.steam.running
	s.steam.mu.Unlock()
	if updating {
		return errors.New("steamcmd 正在更新，请稍后再启动")
	}

	cfg, err := dstpkg.ReadConfig(p.ClusterDir)
	if err != nil {
		return err
	}
	shards, err := dstpkg.DetectShards(p.ClusterDir)
	if err != nil || len(shards) == 0 {
		return fmt.Errorf("集群目录里没有可用的世界: %s", p.ClusterDir)
	}
	if s.dstView(p).Running {
		return errors.New("实例已在运行")
	}

	ports := make([]int, 0, len(shards)+1)
	for _, sh := range shards {
		if sh.Port > 0 {
			ports = append(ports, sh.Port)
		}
	}
	if len(shards) > 1 && cfg.Cluster.MasterPort > 0 {
		ports = append(ports, cfg.Cluster.MasterPort)
	}
	if busy := busyUDPPorts(ports); len(busy) > 0 {
		return fmt.Errorf("UDP 端口已被占用: %v（可能旧服务器还在运行，先在任务管理器结束它）", busy)
	}

	_, exe, err := dstpkg.FindServer(s.paths.Settings.SteamCmdDir)
	if err != nil {
		return err
	}
	// DST resolves data bundles relative to the working directory; it must be
	// the directory holding the executable (bin/bin64), not the game root,
	// otherwise every databundles/*.zip is "skipped" and main.lua fails.
	launchDir := filepath.Dir(exe)

	var started []*unit.Proc
	rollback := func() {
		for _, pr := range started {
			_ = pr.Stop(3 * time.Second)
		}
	}
	for _, sh := range shards {
		proc := s.dstShardProc(p.ID, sh.Name)
		if proc.Running() {
			continue
		}
		args := dstpkg.LaunchArgs(p.ClusterDir, sh.Name)
		if err := proc.StartEnv(exe, args, launchDir, dstpkg.LaunchEnv()); err != nil {
			rollback()
			return fmt.Errorf("%s: %w", filepath.Base(sh.Name), err)
		}
		started = append(started, proc)

		// DST expects the master shard to be up before caves joins it.
		if sh.IsMaster {
			time.Sleep(2 * time.Second)
			if st := proc.Status(); !st.Running {
				rollback()
				if st.LastError == "" {
					return errors.New("Master 启动后立即退出，查看启动日志")
				}
				return fmt.Errorf("Master 启动后退出: %s", st.LastError)
			}
		}
	}
	if len(started) == 0 {
		return errors.New("没有启动任何分片")
	}
	return nil
}

func udpPortFree(port int) bool {
	conn, err := net.ListenPacket("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func busyUDPPorts(ports []int) []int {
	var busy []int
	seen := map[int]bool{}
	for _, port := range ports {
		if port <= 0 || port > 65535 || seen[port] {
			continue
		}
		seen[port] = true
		if !udpPortFree(port) {
			busy = append(busy, port)
		}
	}
	return busy
}

func (s *Server) stopDST(p store.DSTProfile) {
	shards, _ := dstpkg.DetectShards(p.ClusterDir)
	var shardProcs []*unit.Proc
	for _, sh := range shards {
		pr := s.dstShardProc(p.ID, sh.Name)
		if pr.Running() {
			shardProcs = append(shardProcs, pr)
		}
	}
	for _, pr := range shardProcs {
		_ = pr.WriteStdin("c_shutdown()\n")
	}
	if len(shardProcs) > 0 {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			alive := false
			for _, pr := range shardProcs {
				if pr.Running() {
					alive = true
					break
				}
			}
			if !alive {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		for _, pr := range shardProcs {
			if pr.Running() {
				_ = pr.Stop(3 * time.Second)
			}
		}
	}
}

func binName(kind string) string {
	if kind == "server" {
		return "frps"
	}
	return "frpc"
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, format string, args ...any) {
	writeJSON(w, code, map[string]string{"error": fmt.Sprintf(format, args...)})
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"steamcmdDir": s.paths.Settings.SteamCmdDir})
}

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SteamCmdDir string `json:"steamcmdDir"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "invalid body: %v", err)
		return
	}
	s.paths.Settings.SteamCmdDir = strings.TrimSpace(req.SteamCmdDir)
	if err := s.paths.SaveSettings(); err != nil {
		fail(w, http.StatusInternalServerError, "save settings: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"steamcmdDir": s.paths.Settings.SteamCmdDir})
}

func (s *Server) handleSteamStatus(w http.ResponseWriter, r *http.Request) {
	snap := s.steam.snapshot()
	dir := s.paths.Settings.SteamCmdDir
	snap["steamcmdDir"] = dir
	if exe, err := dstpkg.FindSteamCmd(dir); err == nil {
		snap["steamcmd"] = exe
	} else {
		snap["steamcmdError"] = err.Error()
	}
	if root, exe, err := dstpkg.FindServer(dir); err == nil {
		snap["serverFound"] = true
		snap["serverInstall"] = root
		snap["serverExe"] = exe
	} else {
		snap["serverFound"] = false
		snap["serverError"] = err.Error()
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleSteamUpdate(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(s.paths.Settings.SteamCmdDir)
	exe, err := dstpkg.FindSteamCmd(dir)
	if err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	for _, p := range s.dstStore.List() {
		if s.dstView(p).Running {
			fail(w, http.StatusConflict, "有 DST 实例正在运行，请先停止再更新")
			return
		}
	}

	j := s.steam
	j.mu.Lock()
	if j.running {
		j.mu.Unlock()
		fail(w, http.StatusConflict, "steamcmd 正在更新")
		return
	}
	logPath := filepath.Join(s.paths.Logs, "steamcmd.log")
	lf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	j.running = true
	j.success = false
	j.errMsg = ""
	j.started = time.Now()
	j.finished = time.Time{}
	j.lines = nil
	j.pending = ""
	j.logFile = lf
	j.addLocked("=== steamcmd update " + time.Now().Format(time.RFC3339) + " ===")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	j.cancel = cancel
	j.mu.Unlock()

	cmd := unit.CommandContext(ctx, exe, []string{"+login", "anonymous", "+app_update", "343050", "validate", "+quit"}, dir)
	writer := steamWriter{job: j}
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Start(); err != nil {
		cancel()
		j.finish(err)
		fail(w, http.StatusBadRequest, "steamcmd 启动失败: %v", err)
		return
	}
	go func() {
		err := cmd.Wait()
		cancel()
		j.finish(err)
	}()
	writeJSON(w, http.StatusOK, j.snapshot())
}

func (s *Server) handleSteamCmdTest(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(s.paths.Settings.SteamCmdDir)
	exe, err := dstpkg.FindSteamCmd(dir)
	if err != nil {
		fail(w, http.StatusBadRequest, "%v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := unit.CommandContext(ctx, exe, []string{"+quit"}, dir)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		fail(w, http.StatusBadRequest, "steamcmd 执行失败: %v\n%s", err, text)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": text})
}

func (s *Server) handleSteamClear(w http.ResponseWriter, r *http.Request) {
	if err := s.steam.reset(); err != nil {
		fail(w, http.StatusConflict, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
