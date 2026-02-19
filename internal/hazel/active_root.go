package hazel

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func activeRootPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hazel", "active_root"), nil
}

func SetActiveRoot(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return errors.New("active root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	p, err := activeRootPath()
	if err != nil {
		return err
	}
	return writeFileAtomic(p, []byte(abs+"\n"), 0o644)
}

func GetActiveRoot() (string, error) {
	p, err := activeRootPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(string(b))
	if root == "" {
		return "", errors.New("active root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return abs, nil
}
