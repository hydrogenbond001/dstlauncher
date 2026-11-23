package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitMigratesLegacyLayout(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "conf"))
	mustWrite(t, filepath.Join(root, "conf", "panel.json"), `{"listen":"127.0.0.1:7777","autoOpen":false}`)
	mustMkdir(t, filepath.Join(root, "profiles", "p1"))
	mustWrite(t, filepath.Join(root, "profiles", "p1", "meta.json"), `{"id":"p1"}`)
	mustMkdir(t, filepath.Join(root, "logs"))
	mustWrite(t, filepath.Join(root, "logs", "p1.log"), "old log\n")
	mustMkdir(t, filepath.Join(root, "bin"))
	mustWrite(t, filepath.Join(root, "bin", "frpc"), "binary")

	p, err := Init(root)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.Data != filepath.Join(root, "data") {
		t.Fatalf("Data = %q", p.Data)
	}
	if p.Settings.Listen != "127.0.0.1:7777" {
		t.Fatalf("Listen = %q", p.Settings.Listen)
	}
	for _, path := range []string{
		filepath.Join(root, "data", "panel.json"),
		filepath.Join(root, "data", "profiles", "p1", "meta.json"),
		filepath.Join(root, "data", "logs", "p1.log"),
		filepath.Join(root, "data", "bin", "frpc"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing %s: %v", path, err)
		}
	}
	for _, name := range []string{"conf", "profiles", "logs", "bin"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("legacy %s still exists", name)
		}
	}
}

func TestInitFreshLayout(t *testing.T) {
	root := t.TempDir()
	p, err := Init(root)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, dir := range []string{p.Data, p.Profiles, p.DST, p.Logs, p.Bin} {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			t.Errorf("missing dir %s: %v", dir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "data", "panel.json")); err != nil {
		t.Errorf("missing panel.json: %v", err)
	}
	if entries, err := os.ReadDir(root); err != nil {
		t.Fatal(err)
	} else if len(entries) != 1 || entries[0].Name() != "data" {
		t.Errorf("root entries = %v, want only data", entries)
	}
}

func TestInitKeepsExistingDataOverLegacy(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "data"))
	mustWrite(t, filepath.Join(root, "data", "panel.json"), `{"listen":"127.0.0.1:8888"}`)
	mustMkdir(t, filepath.Join(root, "conf"))
	mustWrite(t, filepath.Join(root, "conf", "panel.json"), `{"listen":"127.0.0.1:7777"}`)

	p, err := Init(root)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.Settings.Listen != "127.0.0.1:8888" {
		t.Fatalf("Listen = %q, want data/ value", p.Settings.Listen)
	}
	if _, err := os.Stat(filepath.Join(root, "conf", "panel.json")); err != nil {
		t.Errorf("legacy config should be left intact: %v", err)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
