// Package steamcmd downloads the small SteamCMD bootstrap into a user-chosen
// directory. It never overwrites an existing steamcmd.exe/steamcmd.sh.
package steamcmd

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var client = &http.Client{Timeout: 5 * time.Minute}

func Platform() (exe, url string, err error) {
	switch runtime.GOOS {
	case "windows":
		return "steamcmd.exe", "https://steamcdn-a.akamaihd.net/client/installer/steamcmd.zip", nil
	case "linux":
		return "steamcmd.sh", "https://steamcdn-a.akamaihd.net/client/installer/steamcmd_linux.tar.gz", nil
	default:
		return "", "", fmt.Errorf("当前系统不支持自动下载 SteamCMD: %s", runtime.GOOS)
	}
}

// Install returns (path, alreadyExists, error). Missing directories are
// created; an existing bootstrap executable is left untouched.
func Install(dir string) (string, bool, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", false, errors.New("steamcmd 目录不能为空")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, err
	}
	exe, url, err := Platform()
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(dir, exe)
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		return path, true, nil
	}
	if _, _, err := downloadAndExtract(dir, url); err != nil {
		return "", false, err
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return "", false, fmt.Errorf("解压完成但未找到 %s", exe)
	}
	return path, false, nil
}

func downloadAndExtract(dir, url string) (string, bool, error) {
	tmp, err := os.CreateTemp(dir, ".steamcmd-*")
	if err != nil {
		return "", false, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	resp, err := client.Get(url)
	if err != nil {
		tmp.Close()
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tmp.Close()
		return "", false, fmt.Errorf("下载失败: %s", resp.Status)
	}
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, 200<<20)); err != nil {
		tmp.Close()
		return "", false, err
	}
	if err := tmp.Close(); err != nil {
		return "", false, err
	}

	switch {
	case strings.HasSuffix(url, ".zip"):
		err = unzip(tmpName, dir)
	case strings.HasSuffix(url, ".tar.gz"):
		err = untar(tmpName, dir)
	default:
		err = fmt.Errorf("未知安装包格式: %s", url)
	}
	if err != nil {
		return "", false, err
	}
	return dir, false, nil
}

func unzip(path, dir string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target, err := safeTarget(dir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		if strings.HasSuffix(strings.ToLower(f.Name), ".sh") {
			mode |= 0o755
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

func untar(path, dir string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target, err := safeTarget(dir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			if strings.HasSuffix(strings.ToLower(hdr.Name), ".sh") {
				mode |= 0o755
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			out.Close()
			if copyErr != nil {
				return copyErr
			}
		default:
			// SteamCMD's bootstrap only needs regular files/dirs; skip
			// symlinks and hardlinks rather than risk escaping the target.
			continue
		}
	}
	return nil
}

func safeTarget(root, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." {
		return root, nil
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("安装包含有非法路径: %s", name)
	}
	target := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("安装包含有非法路径: %s", name)
	}
	return target, nil
}
