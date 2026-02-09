package hazel

import (
	"fmt"
	"os"
	"path/filepath"
)

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}
