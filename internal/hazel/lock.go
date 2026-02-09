package hazel

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// withRepoLock enforces single-flight behavior for agent/tick operations.
// This keeps board.yaml and the working tree from being modified concurrently.
func withRepoLock(root string, fn func() error) error {
	p := filepath.Join(hazelDir(root), "lock")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock %s: %w", p, err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}
