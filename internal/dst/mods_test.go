package dst

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleMods = `return {
  -- keep this comment
  ["workshop-111"] = { enabled = true, configuration_options = { x = "y" } },
  ["workshop-222"] = {
    enabled = false,
    configuration_options = { a = 1, b = true },
  },
}
`

func TestParseModOverrides(t *testing.T) {
	entries, _, _, err := ParseModOverrides([]byte(sampleMods))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].ID != "111" || !entries[0].Enabled {
		t.Fatalf("entry 0 = %+v", entries[0])
	}
	if entries[1].ID != "222" || entries[1].Enabled {
		t.Fatalf("entry 1 = %+v", entries[1])
	}
}

func TestSetModEnabledKeepsConfig(t *testing.T) {
	out, err := SetModEnabled([]byte(sampleMods), "222", true)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, _, err := ParseModOverrides(out)
	if err != nil {
		t.Fatal(err)
	}
	if !entries[1].Enabled {
		t.Fatal("enabled not updated")
	}
	if !strings.Contains(string(out), `configuration_options = { a = 1, b = true }`) {
		t.Fatalf("config lost:\n%s", out)
	}
}

func TestEnsureAndRemoveMod(t *testing.T) {
	out, err := EnsureMod([]byte(sampleMods), "333", true)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, _, err := ParseModOverrides(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].ID != "333" {
		t.Fatalf("entries after ensure: %+v", entries)
	}
	out, err = RemoveMod(out, "111")
	if err != nil {
		t.Fatal(err)
	}
	entries, _, _, err = ParseModOverrides(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries after remove: %+v", entries)
	}
	for _, e := range entries {
		if e.ID == "111" {
			t.Fatal("111 still present")
		}
	}
}

func TestEnsureModCreatesFile(t *testing.T) {
	out, err := EnsureMod(nil, "444", false)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, _, err := ParseModOverrides(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != "444" || entries[0].Enabled {
		t.Fatalf("entries: %+v", entries)
	}
}

func TestSetupFileEdits(t *testing.T) {
	in := []byte("-- ServerModSetup(\"1\")\nServerModSetup(\"2\") -- two\n")
	ids := SetupIDs(in)
	if ids["1"] || !ids["2"] {
		t.Fatalf("SetupIDs = %v", ids)
	}
	out := AddSetup(in, "3", "three")
	if !SetupIDs(out)["3"] {
		t.Fatalf("3 not added:\n%s", out)
	}
	if string(AddSetup(out, "3", "")) != string(out) {
		t.Fatal("AddSetup is not idempotent")
	}
	out = RemoveSetup(out, "2")
	if SetupIDs(out)["2"] {
		t.Fatalf("2 not removed:\n%s", out)
	}
	if !strings.Contains(string(out), `-- ServerModSetup("1")`) {
		t.Fatal("commented line was removed")
	}
}

func TestWriteFileBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "modoverrides.lua")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileBackup(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Fatalf("content = %q", got)
	}
	matches, _ := filepath.Glob(path + ".bak-*")
	if len(matches) != 1 {
		t.Fatalf("backup count = %d", len(matches))
	}
	if err := WriteFileBackup(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	matches, _ = filepath.Glob(path + ".bak-*")
	if len(matches) != 1 {
		t.Fatalf("unchanged write created backup: %d", len(matches))
	}
}
