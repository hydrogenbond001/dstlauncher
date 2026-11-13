// Package embedfs carries the platform's frp binaries inside the executable.
// Builds produced by build.sh embed gzipped frpc/frps; a plain `go build` embeds
// only the placeholder and expects binaries in ./bin.
package embedfs

import (
	"compress/gzip"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed all:bin
var content embed.FS

// ExtractBins decompresses embedded binaries into dst, skipping files that
// already exist so users can swap in a newer frp build.
func ExtractBins(dst string) ([]string, error) {
	return extractAll(content, "bin", dst)
}

func extractAll(fsys fs.FS, dir, dst string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var (
		written []string
		errs    []error
	)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".gz") {
			continue
		}
		target := filepath.Join(dst, strings.TrimSuffix(entry.Name(), ".gz"))
		if st, err := os.Stat(target); err == nil && !st.IsDir() {
			continue
		}
		// embed.FS paths are slash-separated on every OS: path.Join, never
		// filepath.Join (which yields "bin\frpc.exe.gz" on Windows).
		if err := extract(fsys, path.Join(dir, entry.Name()), target); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", entry.Name(), err))
			continue
		}
		written = append(written, target)
	}
	return written, errors.Join(errs...)
}

func extract(fsys fs.FS, name, target string) error {
	src, err := fsys.Open(name)
	if err != nil {
		return err
	}
	defer src.Close()
	zr, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()

	tmp, err := os.CreateTemp(filepath.Dir(target), ".extract-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, zr); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}
