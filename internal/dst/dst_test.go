package dst

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindSteamCmdAndServer(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"steamcmd.sh", "steamcmd.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	game := filepath.Join(dir, "steamapps", "common", "Don't Starve Together Dedicated Server")
	rel := serverExeCandidates()[0]
	exe := filepath.Join(game, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd, err := FindSteamCmd(dir)
	if err != nil {
		t.Fatalf("FindSteamCmd: %v", err)
	}
	if filepath.Dir(cmd) != dir {
		t.Errorf("steamcmd = %q, want inside %q", cmd, dir)
	}

	root, path, err := FindServer(dir)
	if err != nil {
		t.Fatalf("FindServer: %v", err)
	}
	if root != game || path != exe {
		t.Errorf("FindServer = (%q, %q), want (%q, %q)", root, path, game, exe)
	}
}

func TestLaunchArgs(t *testing.T) {
	cluster := filepath.Join("data", "Klei", "DoNotStarveTogether", "Cluster_2")
	abs, err := filepath.Abs(cluster)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(abs))
	args := LaunchArgs(cluster, "Master")
	want := []string{
		"-persistent_storage_root", root,
		"-conf_dir", "DoNotStarveTogether",
		"-cluster", "Cluster_2",
		"-shard", "Master",
		"-console",
	}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("LaunchArgs = %v, want %v", args, want)
	}
}

func TestWriteConfigPreservesUnknownKeysAndCRLF(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Master"), 0o755); err != nil {
		t.Fatal(err)
	}
	cluster := strings.Join([]string{
		"[GAMEPLAY]",
		"game_mode = survival",
		"max_players = 6",
		"unknown_keep_me = yes",
		"",
		"[NETWORK]",
		"cluster_name = old",
		"cluster_cloud_id = 7ACD",
		"",
	}, "\r\n")
	if err := os.WriteFile(filepath.Join(dir, "cluster.ini"), []byte(cluster), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Master", "server.ini"),
		[]byte("[NETWORK]\r\nserver_port = 10999\r\n\r\n[SHARD]\r\nis_master = true\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	view, err := ReadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	view.Cluster.Name = "new name"
	view.Cluster.MaxPlayers = 8
	if err := WriteConfig(view); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "cluster.ini"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "unknown_keep_me = yes") {
		t.Errorf("unknown key was dropped:\n%s", text)
	}
	if !strings.Contains(text, "cluster_cloud_id = 7ACD") {
		t.Errorf("unknown network key was dropped:\n%s", text)
	}
	if !strings.Contains(text, "cluster_name = new name") || !strings.Contains(text, "max_players = 8") {
		t.Errorf("updates not written:\n%s", text)
	}
	if !strings.Contains(text, "\r\n") || strings.Count(text, "\n") != strings.Count(text, "\r\n") {
		t.Errorf("CRLF was not preserved:\n%q", text)
	}
}

func TestDetectShards(t *testing.T) {
	dir := t.TempDir()
	for name, master := range map[string]bool{"Master": true, "Caves": false} {
		sub := filepath.Join(dir, name)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		ini := "[NETWORK]\nserver_port = " + map[bool]string{true: "10999", false: "10998"}[master] +
			"\n\n[SHARD]\nis_master = " + map[bool]string{true: "true", false: "false"}[master] + "\n"
		if err := os.WriteFile(filepath.Join(sub, "server.ini"), []byte(ini), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	shards, err := DetectShards(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 2 || !shards[0].IsMaster || shards[0].Name != "Master" {
		t.Fatalf("unexpected shards: %+v", shards)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
