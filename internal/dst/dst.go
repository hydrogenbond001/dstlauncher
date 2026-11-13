// Package dst understands an existing Don't Starve Together cluster directory:
// it detects shards, reads/writes common INI fields, and parses the launch bat.
// It never creates or regenerates the cluster layout.
package dst

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

type Shard struct {
	Name     string `json:"name"`
	Dir      string `json:"dir"`
	IsMaster bool   `json:"isMaster"`
	Port     int    `json:"port"`
}

type ClusterConfig struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Password       string `json:"password"`
	Language       string `json:"language"`
	GameMode       string `json:"gameMode"`
	MaxPlayers     int    `json:"maxPlayers"`
	PvP            bool   `json:"pvp"`
	PauseWhenEmpty bool   `json:"pauseWhenEmpty"`
	MasterPort     int    `json:"masterPort"`
	ClusterKey     string `json:"clusterKey"`
	ShardEnabled   bool   `json:"shardEnabled"`
	ConsoleEnabled bool   `json:"consoleEnabled"`
}

type ShardView struct {
	Name             string `json:"name"`
	Dir              string `json:"dir"`
	IsMaster         bool   `json:"isMaster"`
	Port             int    `json:"port"`
	MasterServerPort int    `json:"masterServerPort"`
	AuthPort         int    `json:"authPort"`
}

type ConfigView struct {
	ClusterDir string        `json:"clusterDir"`
	Cluster    ClusterConfig `json:"cluster"`
	Shards     []ShardView   `json:"shards"`
	Token      string        `json:"token"`
	AdminList  string        `json:"adminList"`
	Files      []string      `json:"files"`
}

// DetectShards returns subdirectories that hold a server.ini, master first.
func DetectShards(clusterDir string) ([]Shard, error) {
	entries, err := os.ReadDir(clusterDir)
	if err != nil {
		return nil, err
	}
	var shards []Shard
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(clusterDir, e.Name())
		iniPath := filepath.Join(dir, "server.ini")
		if _, err := os.Stat(iniPath); err != nil {
			continue
		}
		ini, err := loadINI(iniPath)
		if err != nil {
			continue
		}
		port, _ := strconv.Atoi(ini.get("NETWORK", "server_port"))
		master := strings.EqualFold(ini.get("SHARD", "is_master"), "true")
		shards = append(shards, Shard{Name: e.Name(), Dir: dir, IsMaster: master, Port: port})
	}
	sort.Slice(shards, func(i, j int) bool {
		if shards[i].IsMaster != shards[j].IsMaster {
			return shards[i].IsMaster
		}
		return strings.ToLower(shards[i].Name) < strings.ToLower(shards[j].Name)
	})
	return shards, nil
}

func ValidateClusterDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("集群目录不能为空")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("集群目录不存在: %s", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "cluster.ini")); err != nil {
		return fmt.Errorf("目录里没有 cluster.ini: %s", dir)
	}
	shards, err := DetectShards(dir)
	if err != nil {
		return err
	}
	if len(shards) == 0 {
		return errors.New("目录里没有包含 server.ini 的世界文件夹（Master/Caves）")
	}
	return nil
}

func ReadConfig(clusterDir string) (ConfigView, error) {
	view := ConfigView{ClusterDir: clusterDir, Cluster: defaultCluster()}
	if err := ValidateClusterDir(clusterDir); err != nil {
		return view, err
	}

	clusterINI := filepath.Join(clusterDir, "cluster.ini")
	ini, err := loadINI(clusterINI)
	if err != nil {
		return view, err
	}
	c := &view.Cluster
	c.Name = ini.get("NETWORK", "cluster_name")
	c.Description = ini.get("NETWORK", "cluster_description")
	c.Password = ini.get("NETWORK", "cluster_password")
	c.Language = ini.get("NETWORK", "cluster_language")
	c.GameMode = ini.get("GAMEPLAY", "game_mode")
	c.MaxPlayers = atoiDefault(ini.get("GAMEPLAY", "max_players"), 6)
	c.PvP = boolValue(ini.get("GAMEPLAY", "pvp"))
	c.PauseWhenEmpty = boolValue(ini.get("GAMEPLAY", "pause_when_empty"))
	c.MasterPort = atoiDefault(ini.get("SHARD", "master_port"), 10888)
	c.ClusterKey = ini.get("SHARD", "cluster_key")
	c.ShardEnabled = boolValue(ini.get("SHARD", "shard_enabled"))
	c.ConsoleEnabled = boolValue(ini.get("MISC", "console_enabled"))

	shards, err := DetectShards(clusterDir)
	if err != nil {
		return view, err
	}
	for _, sh := range shards {
		sv := ShardView{Name: sh.Name, Dir: sh.Dir, IsMaster: sh.IsMaster, Port: sh.Port}
		if ini, err := loadINI(filepath.Join(sh.Dir, "server.ini")); err == nil {
			sv.MasterServerPort = atoiDefault(ini.get("STEAM", "master_server_port"), 0)
			sv.AuthPort = atoiDefault(ini.get("STEAM", "authentication_port"), 0)
		}
		view.Shards = append(view.Shards, sv)
	}
	view.Token = readText(filepath.Join(clusterDir, "cluster_token.txt"))
	view.AdminList = readText(filepath.Join(clusterDir, "adminlist.txt"))
	view.Files = []string{
		clusterINI,
		filepath.Join(clusterDir, "cluster_token.txt"),
		filepath.Join(clusterDir, "adminlist.txt"),
	}
	for _, sh := range view.Shards {
		view.Files = append(view.Files, filepath.Join(sh.Dir, "server.ini"))
	}
	return view, nil
}

// WriteConfig patches only the known keys, preserving comments and any extra
// keys the cluster already has.
func WriteConfig(view ConfigView) error {
	if err := ValidateClusterDir(view.ClusterDir); err != nil {
		return err
	}
	clusterINI := filepath.Join(view.ClusterDir, "cluster.ini")
	ini, err := loadINI(clusterINI)
	if err != nil {
		return err
	}
	c := view.Cluster
	ini.set("NETWORK", "cluster_name", c.Name)
	ini.set("NETWORK", "cluster_description", c.Description)
	ini.set("NETWORK", "cluster_password", c.Password)
	ini.set("NETWORK", "cluster_language", c.Language)
	ini.set("GAMEPLAY", "game_mode", c.GameMode)
	ini.set("GAMEPLAY", "max_players", strconv.Itoa(c.MaxPlayers))
	ini.set("GAMEPLAY", "pvp", strconv.FormatBool(c.PvP))
	ini.set("GAMEPLAY", "pause_when_empty", strconv.FormatBool(c.PauseWhenEmpty))
	ini.set("SHARD", "master_port", strconv.Itoa(c.MasterPort))
	ini.set("SHARD", "cluster_key", c.ClusterKey)
	ini.set("SHARD", "shard_enabled", strconv.FormatBool(c.ShardEnabled))
	ini.set("MISC", "console_enabled", strconv.FormatBool(c.ConsoleEnabled))
	if err := ini.save(clusterINI); err != nil {
		return err
	}

	for _, sh := range view.Shards {
		path := filepath.Join(sh.Dir, "server.ini")
		ini, err := loadINI(path)
		if err != nil {
			return err
		}
		ini.set("NETWORK", "server_port", strconv.Itoa(sh.Port))
		ini.set("SHARD", "is_master", strconv.FormatBool(sh.IsMaster))
		if sh.MasterServerPort > 0 {
			ini.set("STEAM", "master_server_port", strconv.Itoa(sh.MasterServerPort))
		}
		if sh.AuthPort > 0 {
			ini.set("STEAM", "authentication_port", strconv.Itoa(sh.AuthPort))
		}
		if err := ini.save(path); err != nil {
			return err
		}
	}

	if err := writeText(filepath.Join(view.ClusterDir, "cluster_token.txt"), view.Token); err != nil {
		return err
	}
	return writeText(filepath.Join(view.ClusterDir, "adminlist.txt"), view.AdminList)
}

func FindSteamCmd(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", errors.New("未设置 steamcmd 目录")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("steamcmd 目录不存在: %s", dir)
	}
	for _, name := range []string{"steamcmd.exe", "steamcmd.sh"} {
		path := filepath.Join(dir, name)
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("目录里没有 steamcmd.exe/steamcmd.sh: %s", dir)
}

// FindServer locates the dedicated server installed by steamcmd:
// <steamcmd>/steamapps/common/Don't Starve Together Dedicated Server/...
func FindServer(steamcmdDir string) (installRoot, exe string, err error) {
	steamcmdDir = strings.TrimSpace(steamcmdDir)
	if steamcmdDir == "" {
		return "", "", errors.New("未设置 steamcmd 目录")
	}
	roots := []string{steamcmdDir}
	common := filepath.Join(steamcmdDir, "steamapps", "common")
	if entries, err := os.ReadDir(common); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.Contains(strings.ToLower(e.Name()), "starve") {
				roots = append(roots, filepath.Join(common, e.Name()))
			}
		}
	}
	for _, root := range roots {
		for _, rel := range serverExeCandidates() {
			path := filepath.Join(root, filepath.FromSlash(rel))
			if st, err := os.Stat(path); err == nil && !st.IsDir() {
				return root, path, nil
			}
		}
	}
	return "", "", fmt.Errorf("未找到 Don't Starve Together Dedicated Server，请先用 steamcmd 安装 343050")
}

func serverExeCandidates() []string {
	if runtime.GOOS == "windows" {
		return []string{
			"bin64/dontstarve_dedicated_server_nullrenderer_x64.exe",
			"bin64/dontstarve_dedicated_server_nullrenderer.exe",
			"bin/dontstarve_dedicated_server_nullrenderer.exe",
		}
	}
	return []string{
		"bin/dontstarve_dedicated_server_nullrenderer",
		"bin64/dontstarve_dedicated_server_nullrenderer",
	}
}

// LaunchArgs builds the shard command from the cluster directory, so the
// panel never needs a bat and does not depend on its own location.
//
// DST's real layout is <persistent_storage_root>/<conf_dir>/<accountID>/<cluster>
// where <accountID> is inserted by the game itself and must NOT be passed as
// -conf_dir; doing so makes the server look in <accountID>/<accountID>/<cluster>
// and fail with a tier0 assertion during early shutdown.
func LaunchArgs(clusterDir, shard string) []string {
	abs, err := filepath.Abs(clusterDir)
	if err == nil {
		clusterDir = abs
	}
	cluster := filepath.Base(clusterDir)
	confDir := filepath.Base(filepath.Dir(clusterDir))
	root := filepath.Dir(filepath.Dir(clusterDir))
	if numericName(confDir) {
		confDir = filepath.Base(root)
		root = filepath.Dir(root)
	}
	args := []string{
		"-persistent_storage_root", root,
		"-conf_dir", confDir,
		"-cluster", cluster,
	}
	if shard != "" {
		args = append(args, "-shard", shard)
	}
	return append(args, "-console")
}

func numericName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func LaunchEnv() []string {
	return []string{"SteamAppId=322330", "SteamGameId=322330"}
}

/* ---- ini ---- */

type iniFile struct {
	lines []string
	eol   string
}

func loadINI(path string) (*iniFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	eol := "\n"
	if strings.Contains(string(data), "\r\n") {
		eol = "\r\n"
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return &iniFile{lines: lines, eol: eol}, nil
}

func (f *iniFile) sectionRange(section string) (int, int, bool) {
	start := -1
	for i, line := range f.lines {
		name, ok := sectionName(line)
		if !ok {
			continue
		}
		if start >= 0 {
			return start + 1, i, true
		}
		if strings.EqualFold(name, section) {
			start = i
		}
	}
	if start >= 0 {
		return start + 1, len(f.lines), true
	}
	return 0, 0, false
}

func (f *iniFile) get(section, key string) string {
	start, end, ok := f.sectionRange(section)
	if !ok {
		return ""
	}
	for i := start; i < end; i++ {
		if k, v, ok := keyValue(f.lines[i]); ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func (f *iniFile) set(section, key, value string) {
	start, end, ok := f.sectionRange(section)
	if !ok {
		if len(f.lines) > 0 && strings.TrimSpace(f.lines[len(f.lines)-1]) != "" {
			f.lines = append(f.lines, "")
		}
		f.lines = append(f.lines, "["+section+"]", key+" = "+value)
		return
	}
	for i := start; i < end; i++ {
		if k, _, ok := keyValue(f.lines[i]); ok && strings.EqualFold(k, key) {
			f.lines[i] = leadingSpace(f.lines[i]) + key + " = " + value
			return
		}
	}
	insert := end
	for insert > start && strings.TrimSpace(f.lines[insert-1]) == "" {
		insert--
	}
	f.lines = append(f.lines, "")
	copy(f.lines[insert+1:], f.lines[insert:])
	f.lines[insert] = key + " = " + value
}

func (f *iniFile) save(path string) error {
	return os.WriteFile(path, []byte(strings.Join(f.lines, f.eol)+f.eol), 0o644)
}

func sectionName(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return "", false
	}
	return strings.TrimSpace(s[1 : len(s)-1]), true
}

func keyValue(line string) (string, string, bool) {
	s := strings.TrimSpace(line)
	if s == "" || s[0] == ';' || s[0] == '#' || s[0] == '[' {
		return "", "", false
	}
	k, v, ok := strings.Cut(s, "=")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(k), strings.TrimSpace(v), true
}

func leadingSpace(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

/* ---- small helpers ---- */

func defaultCluster() ClusterConfig {
	return ClusterConfig{MaxPlayers: 6, PauseWhenEmpty: true, ShardEnabled: true, ConsoleEnabled: true}
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

func boolValue(s string) bool {
	return strings.EqualFold(strings.TrimSpace(s), "true")
}

func readText(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(data), "\r\n")
}

func writeText(path, content string) error {
	content = strings.TrimRight(content, "\r\n")
	if content == "" {
		return os.WriteFile(path, nil, 0o644)
	}
	return os.WriteFile(path, []byte(content+"\n"), 0o644)
}

type token struct {
	s      string
	quoted bool
}

func splitCmdLine(line string) []token {
	var (
		out    []token
		buf    strings.Builder
		quoted bool
		inQ    bool
	)
	flush := func() {
		if buf.Len() > 0 || quoted {
			out = append(out, token{s: buf.String(), quoted: quoted})
			buf.Reset()
			quoted = false
		}
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '"':
			inQ = !inQ
			quoted = true
		case c == ' ' || c == '\t':
			if inQ {
				buf.WriteByte(c)
			} else {
				flush()
			}
		default:
			buf.WriteByte(c)
		}
	}
	flush()
	return out
}
