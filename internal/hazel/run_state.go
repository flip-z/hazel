package hazel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type RunState struct {
	Running   bool      `json:"running"`
	TaskID    string    `json:"task_id,omitempty"`
	Mode      string    `json:"mode,omitempty"` // plan|implement
	LogPath   string    `json:"log_path,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	ExitCode  *int      `json:"exit_code,omitempty"`
	Error     string    `json:"error,omitempty"`
}

func runStatePath(root string) string {
	return filepath.Join(hazelDir(root), "run_state.json")
}

func readRunState(root string) (*RunState, error) {
	b, err := os.ReadFile(runStatePath(root))
	if err != nil {
		return nil, err
	}
	var st RunState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func writeRunState(root string, st *RunState) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeFileAtomic(runStatePath(root), b, 0o644)
}

func runMetaPathForLog(logPath string) string {
	if logPath == "" {
		return ""
	}
	ext := filepath.Ext(logPath)
	if ext == "" {
		return logPath + ".json"
	}
	return logPath[:len(logPath)-len(ext)] + ".json"
}
