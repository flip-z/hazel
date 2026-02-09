package hazel

import (
	"context"
	"fmt"
	"time"
)

// Plan runs the configured agent in "plan" mode for a given task.
// It must not edit task.md; it should write planning output to impl.md.
func Plan(ctx context.Context, root string, taskID string) (*RunResult, error) {
	var outRes *RunResult
	var outErr error
	err := withRepoLock(root, func() error {
		outRes, outErr = planLocked(ctx, root, taskID)
		return outErr
	})
	if err != nil {
		return nil, err
	}
	return outRes, nil
}

func planLocked(ctx context.Context, root string, taskID string) (*RunResult, error) {
	var cfg Config
	if err := readYAMLFile(configPath(root), &cfg); err != nil {
		return nil, err
	}
	if cfg.AgentCommand == "" {
		return nil, fmt.Errorf("agent_command is not configured in .hazel/config.yaml")
	}

	var b Board
	if err := readYAMLFile(boardPath(root), &b); err != nil {
		return nil, err
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}

	var t *BoardTask
	for _, x := range b.Tasks {
		if x.ID == taskID {
			t = x
			break
		}
	}
	if t == nil {
		return nil, fmt.Errorf("task not found on board: %s", taskID)
	}

	now := time.Now()
	if err := ensureTaskScaffold(root, taskID); err != nil {
		return nil, err
	}
	if err := writeAgentPacket(root, t, now); err != nil {
		return nil, err
	}

	lp, _ := computeRunLogPath(root, cfg, now, taskID)
	_ = writeRunState(root, &RunState{
		Running:   true,
		TaskID:    taskID,
		Mode:      "plan",
		LogPath:   lp,
		StartedAt: now,
	})

	exit, logPath, err := runAgentCommandMode(ctx, root, cfg, taskID, now, "plan", lp)
	if err != nil {
		return nil, err
	}
	if logPath != "" {
		_ = writeFileAtomic(runMetaPathForLog(logPath), mustJSONIndent(map[string]any{
			"task_id":    taskID,
			"mode":       "plan",
			"started_at": now,
			"ended_at":   time.Now(),
			"exit_code":  exit,
			"log_path":   logPath,
		}), 0o644)
	}
	_ = writeRunState(root, &RunState{
		Running:   false,
		TaskID:    taskID,
		Mode:      "plan",
		LogPath:   logPath,
		StartedAt: now,
		EndedAt:   time.Now(),
		ExitCode:  &exit,
	})
	return &RunResult{
		DispatchedTaskID: taskID,
		AgentExitCode:    &exit,
		RunLogPath:       logPath,
	}, nil
}
