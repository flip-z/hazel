package hazel

import (
	"encoding/json"
	"os"
	"syscall"
	"time"
)

type ServerState struct {
	PID       int       `json:"pid"`
	Addr      string    `json:"addr"`
	StartedAt time.Time `json:"started_at"`
}

func readServerState(root string) (*ServerState, error) {
	b, err := os.ReadFile(serverStatePath(root))
	if err != nil {
		return nil, err
	}
	var st ServerState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func writeServerState(root string, st *ServerState) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeFileAtomic(serverStatePath(root), b, 0o644)
}

func clearServerState(root string) error {
	_ = os.Remove(serverStatePath(root))
	return nil
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}
