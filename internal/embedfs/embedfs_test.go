package embedfs

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"
)

func gzipped(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestExtractAll uses a slash-only fs.FS. On Windows, using filepath.Join for
// the FS path makes this fail with "bin\frpc.exe.gz: file does not exist".
func TestExtractAll(t *testing.T) {
	fsys := fstest.MapFS{
		"bin/frpc.exe.gz": &fstest.MapFile{Data: gzipped(t, "frpc-binary")},
		"bin/frps.gz":     &fstest.MapFile{Data: gzipped(t, "frps-binary")},
		"bin/README.txt":  &fstest.MapFile{Data: []byte("not a binary")},
	}
	dst := t.TempDir()

	written, err := extractAll(fsys, "bin", dst)
	if err != nil {
		t.Fatalf("extractAll: %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("extracted %d files, want 2: %v", len(written), written)
	}

	for name, want := range map[string]string{
		"frpc.exe": "frpc-binary",
		"frps":     "frps-binary",
	} {
		got, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
		if runtime.GOOS != "windows" {
			st, err := os.Stat(filepath.Join(dst, name))
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm()&0o100 == 0 {
				t.Errorf("%s is not executable: %v", name, st.Mode())
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "README.txt")); !os.IsNotExist(err) {
		t.Errorf("non-.gz file should not be extracted")
	}
}

func TestExtractAllKeepsExistingBinary(t *testing.T) {
	dst := t.TempDir()
	existing := filepath.Join(dst, "frpc")
	if err := os.WriteFile(existing, []byte("user-replaced"), 0o755); err != nil {
		t.Fatal(err)
	}

	fsys := fstest.MapFS{
		"bin/frpc.gz": &fstest.MapFile{Data: gzipped(t, "embedded")},
	}
	written, err := extractAll(fsys, "bin", dst)
	if err != nil {
		t.Fatalf("extractAll: %v", err)
	}
	if len(written) != 0 {
		t.Fatalf("rewrote existing binary: %v", written)
	}
	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "user-replaced" {
		t.Fatalf("existing binary was overwritten: %q", got)
	}
}
