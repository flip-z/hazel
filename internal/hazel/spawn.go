package hazel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type SpawnOptions struct {
	PortOverride int
}

func SpawnBackgroundServer(ctx context.Context, root string, opt SpawnOptions) (pid int, addr string, err error) {
	_ = ctx

	// If a server is already running, return its state.
	if st, err := readServerState(root); err == nil && pidAlive(st.PID) {
		return st.PID, st.Addr, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return 0, "", err
	}

	args := []string{"up", "--foreground"}
	if opt.PortOverride != 0 {
		args = append(args, "--port", fmtInt(opt.PortOverride))
	}

	cmd := exec.Command(exe, args...)
	cmd.Dir = root
	cmd.Env = os.Environ()

	// Keep server output somewhere predictable (useful for debugging background starts).
	logPath := filepath.Join(hazelDir(root), "server.log")
	_ = ensureDir(filepath.Dir(logPath))
	if f, ferr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); ferr == nil {
		cmd.Stdout = f
		cmd.Stderr = f
		// Don't close f here; child process owns it after Start().
	}
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return 0, "", err
	}

	// Wait briefly for the child to write server state.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, err := readServerState(root)
		if err == nil && st.PID != 0 && st.Addr != "" && pidAlive(st.PID) {
			return st.PID, st.Addr, nil
		}
		// If the child died quickly, surface logs.
		if !pidAlive(cmd.Process.Pid) {
			return cmd.Process.Pid, "", fmt.Errorf("server exited; see %s", logPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cmd.Process.Pid, "", fmt.Errorf("server did not start (no state file written); see %s", logPath)
}
