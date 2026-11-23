package app

import (
	"fmt"
	"os"
	"path/filepath"
)

// migrateLegacy moves state from the old scattered layout into data/.
// It only fills missing destinations, so a partially migrated tree is safe.
func migrateLegacy(root, data string) error {
	for _, name := range []string{"profiles", "dst", "logs", "bin"} {
		src := filepath.Join(root, name)
		dst := filepath.Join(data, name)
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if _, err := os.Stat(dst); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("migrate %s: %w", src, err)
		}
	}

	legacyConfig := filepath.Join(root, "conf", "panel.json")
	if _, err := os.Stat(legacyConfig); err == nil {
		dst := filepath.Join(data, "panel.json")
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			if err := os.Rename(legacyConfig, dst); err != nil {
				return fmt.Errorf("migrate %s: %w", legacyConfig, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	// Remove the old empty directories; non-empty ones are left untouched.
	for _, name := range []string{"conf", "profiles", "dst", "logs", "bin"} {
		_ = os.Remove(filepath.Join(root, name))
	}
	return nil
}
