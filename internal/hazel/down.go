package hazel

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"
)

type DownOptions struct {
	Force bool
}

type DownResult struct {
	WasRunning bool
	PID        int
}

func Down(ctx context.Context, root string, opt DownOptions) (*DownResult, error) {
	_ = ctx

	st, err := readServerState(root)
	if err != nil {
		if os.IsNotExist(err) {
			return &DownResult{WasRunning: false}, nil
		}
		return nil, err
	}
	if st.PID == 0 {
		_ = clearServerState(root)
		return &DownResult{WasRunning: false}, nil
	}
	if !pidAlive(st.PID) {
		_ = clearServerState(root)
		return &DownResult{WasRunning: false}, nil
	}

	sig := syscall.SIGTERM
	if opt.Force {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(st.PID, sig); err != nil {
		// If it's already gone, treat as success.
		if err == syscall.ESRCH {
			_ = clearServerState(root)
			return &DownResult{WasRunning: true, PID: st.PID}, nil
		}
		return nil, fmt.Errorf("kill %d: %w", st.PID, err)
	}

	// Wait for process to exit.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(st.PID) {
			_ = clearServerState(root)
			return &DownResult{WasRunning: true, PID: st.PID}, nil
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !opt.Force {
		// Escalate.
		_ = syscall.Kill(st.PID, syscall.SIGKILL)
		time.Sleep(100 * time.Millisecond)
	}
	_ = clearServerState(root)
	return &DownResult{WasRunning: true, PID: st.PID}, nil
}

