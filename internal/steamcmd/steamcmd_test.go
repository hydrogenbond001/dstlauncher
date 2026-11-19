package steamcmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func bootstrapName() string {
	if runtime.GOOS == "windows" {
		return "steamcmd.exe"
	}
	return "steamcmd.sh"
}

func TestInstallSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	name := bootstrapName()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, already, err := Install(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !already || filepath.Base(path) != name {
		t.Fatalf("Install = (%q, %v), want existing %s", path, already, name)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "existing" {
		t.Fatalf("existing file was modified: %q, %v", data, err)
	}
}

func TestUntar(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{"steamcmd.sh": "#!/bin/sh\n", "linux32/steamcmd": "bin"}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bootstrap.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := untar(path, dir); err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	st, err := os.Stat(filepath.Join(dir, "steamcmd.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("steamcmd.sh is not executable: %v", st.Mode())
	}
}

func TestUnzip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("steamcmd.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("exe")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bootstrap.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := unzip(path, dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "steamcmd.exe"))
	if err != nil || string(got) != "exe" {
		t.Fatalf("unzip = %q, %v", got, err)
	}
}

func TestSafeTargetRejectsTraversal(t *testing.T) {
	if _, err := safeTarget(t.TempDir(), "../evil"); err == nil {
		t.Fatal("expected traversal error")
	}
	if _, err := safeTarget(t.TempDir(), "linux32/../../evil"); err == nil {
		t.Fatal("expected traversal error")
	}
}
