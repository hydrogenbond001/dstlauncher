// Package app owns the portable on-disk layout and the panel settings file.
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type Settings struct {
	Listen      string `json:"listen"`
	AutoOpen    bool   `json:"autoOpen"`
	SteamCmdDir string `json:"steamcmdDir,omitempty"`
	SteamAPIKey string `json:"steamApiKey,omitempty"`
	Proxy       string `json:"proxy,omitempty"`
}

// Paths is the portable layout rooted at the directory holding the executable.
//
//	<root>/frp-console(.exe)
//	<root>/conf/panel.json
//	<root>/profiles/<id>/{frpc|frps}.toml, meta.json
//	<root>/dst/<id>/{meta.json, config/}
//	<root>/logs/<id>.log, dst-<id>.log
//	<root>/bin/frpc(.exe), frps(.exe)
type Paths struct {
	Root     string
	Conf     string
	Profiles string
	DST      string
	Logs     string
	Bin      string

	Settings     Settings
	settingsPath string
}

func Init(root string) (*Paths, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	p := &Paths{
		Root:         abs,
		Conf:         filepath.Join(abs, "conf"),
		Profiles:     filepath.Join(abs, "profiles"),
		DST:          filepath.Join(abs, "dst"),
		Logs:         filepath.Join(abs, "logs"),
		Bin:          filepath.Join(abs, "bin"),
		settingsPath: filepath.Join(abs, "conf", "panel.json"),
	}
	for _, dir := range []string{p.Conf, p.Profiles, p.DST, p.Logs, p.Bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	p.Settings = Settings{Listen: "127.0.0.1:7600", AutoOpen: true}
	if data, err := os.ReadFile(p.settingsPath); err == nil {
		if err := json.Unmarshal(data, &p.Settings); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p.settingsPath, err)
		}
		if p.Settings.Listen == "" {
			p.Settings.Listen = "127.0.0.1:7600"
		}
	} else if os.IsNotExist(err) {
		if err := p.SaveSettings(); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	return p, nil
}

func (p *Paths) SaveSettings() error {
	data, err := json.MarshalIndent(p.Settings, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(p.settingsPath, append(data, '\n'), 0o644)
}

// FindBin looks for frpc/frps next to the executable, in bin/, then in PATH.
func (p *Paths) FindBin(kind string) (string, error) {
	name := kind
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidates := []string{
		filepath.Join(p.Bin, name),
		filepath.Join(p.Root, name),
	}
	if found, err := exec.LookPath(name); err == nil {
		candidates = append(candidates, found)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("%s not found, expected %s", name, candidates[0])
}

func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
