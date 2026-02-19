package hazel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type RunOptions struct {
	DryRun bool
}

type RunResult struct {
	DispatchedTaskID string
	AgentExitCode    *int
	RunLogPath       string
}

func RunTick(ctx context.Context, root string, opt RunOptions) (*RunResult, error) {
	var outRes *RunResult
	var outErr error
	err := withRepoLock(root, func() error {
		outRes, outErr = runTickLocked(ctx, root, opt)
		return outErr
	})
	if err != nil {
		return nil, err
	}
	return outRes, nil
}

func runTickLocked(ctx context.Context, root string, opt RunOptions) (*RunResult, error) {
	var cfg Config
	if err := readYAMLFile(configPath(root), &cfg); err != nil {
		return nil, err
	}
	if cfg.Version == 0 {
		cfg = defaultConfig()
	}
	if agentCommandForMode(cfg, "implement") == "" && !opt.DryRun {
		return nil, fmt.Errorf("agent_command is not configured in .hazel/config.yaml")
	}

	var b Board
	if err := readYAMLFile(boardPath(root), &b); err != nil {
		return nil, err
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}

	now := time.Now()

	if cfg.EnableEnrichment && !opt.DryRun {
		if err := runEnrichment(root, &b, now); err != nil {
			return nil, err
		}
	}

	next := selectNextReadyFromFS(root, b.Tasks)
	if next == nil {
		return &RunResult{}, nil
	}

	if !opt.DryRun {
		next.Status = StatusActive
		next.UpdatedAt = now
		if err := writeYAMLFile(boardPath(root), &b); err != nil {
			return nil, err
		}
		if err := ensureTaskScaffold(root, next.ID); err != nil {
			return nil, err
		}
		if err := writeAgentPacket(root, next, now); err != nil {
			return nil, err
		}
		if _, err := writePromptPacket(root, next, "implement", now); err != nil {
			return nil, err
		}
	}

	res := &RunResult{DispatchedTaskID: next.ID}

	if opt.DryRun {
		return res, nil
	}

	lp, _ := computeRunLogPath(root, cfg, now, next.ID)
	// Mark as running (best-effort; never breaks the tick).
	_ = writeRunState(root, &RunState{
		Running:   true,
		TaskID:    next.ID,
		Mode:      "implement",
		LogPath:   lp,
		StartedAt: now,
	})

	exit, logPath, err := runAgentCommand(ctx, root, cfg, next.ID, now)
	if err != nil {
		// Still reconcile state below.
		exit = 1
	}
	res.AgentExitCode = &exit
	res.RunLogPath = logPath

	// Persist run metadata alongside the log for UI browsing (best-effort).
	if logPath != "" {
		jsonSummary := summarizeJSONEventsFromLog(logPath)
		_ = writeFileAtomic(runMetaPathForLog(logPath), mustJSONIndent(map[string]any{
			"task_id":      next.ID,
			"mode":         "implement",
			"started_at":   now,
			"ended_at":     time.Now(),
			"exit_code":    exit,
			"log_path":     logPath,
			"dispatched":   true,
			"hazel_root":   root,
			"board_path":   boardPath(root),
			"config_path":  configPath(root),
			"json_summary": jsonSummary,
		}), 0o644)
	}

	_ = writeRunState(root, &RunState{
		Running:   false,
		TaskID:    next.ID,
		Mode:      "implement",
		LogPath:   logPath,
		StartedAt: now,
		EndedAt:   time.Now(),
		ExitCode:  &exit,
	})

	// Consolidated lifecycle: any completed agent run ends in REVIEW.
	var b2 Board
	if rerr := readYAMLFile(boardPath(root), &b2); rerr == nil {
		if vErr := b2.Validate(); vErr == nil {
			for _, t := range b2.Tasks {
				if t.ID == next.ID {
					t.Status = StatusReview
					t.UpdatedAt = time.Now()
					break
				}
			}
			_ = writeYAMLFile(boardPath(root), &b2)
		}
	}

	return res, nil
}

func mustJSONIndent(v any) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	if len(b) == 0 {
		return []byte("{}\n")
	}
	if b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	return b
}

func runEnrichment(root string, b *Board, now time.Time) error {
	// Notes/spec files are no longer part of Hazel's core layout.
	// Keep enrichment as a no-op stub until a new agent-owned channel is defined.
	_ = root
	_ = b
	_ = now
	return nil
}

func ensureTaskScaffold(root, id string) error {
	return ensureTaskScaffoldWithColor(root, id, defaultColorKeyForID(id))
}

func ensureTaskScaffoldWithColor(root, id, colorKey string) error {
	td := taskDir(root, id)
	// Create markdown files if missing. Hazel never overwrites.
	files := map[string]string{
		filepath.Join(td, "task.md"): templateTaskMD,
		filepath.Join(td, "impl.md"): templateImplMD,
	}
	for p, body := range files {
		if !exists(p) {
			if err := writeFileAtomic(p, []byte(body), 0o644); err != nil {
				return err
			}
		}
	}

	// Ensure task.md has a color set (stored in frontmatter).
	if err := ensureTaskColor(root, id, colorKey); err != nil {
		return err
	}
	return nil
}

func writeAgentPacket(root string, t *BoardTask, now time.Time) error {
	p := taskFile(root, t.ID, "agent_packet.md")
	body := fmt.Sprintf(`# Agent Packet

Task: %s
Title: %s
Status: %s
Timestamp: %s

Files:

- %s
- %s
- %s
`,
		t.ID, t.Title, t.Status, now.Format(time.RFC3339),
		rel(root, taskFile(root, t.ID, "task.md")),
		rel(root, taskFile(root, t.ID, "impl.md")),
		rel(root, taskFile(root, t.ID, planProposalFile)),
	)
	return writeFileAtomic(p, []byte(body), 0o644)
}

func runAgentCommand(ctx context.Context, root string, cfg Config, taskID string, now time.Time) (exit int, logPath string, err error) {
	runLogPath, err := computeRunLogPath(root, cfg, now, taskID)
	if err != nil {
		return 0, "", err
	}
	return runAgentCommandMode(ctx, root, cfg, taskID, now, "implement", runLogPath)
}

func computeRunLogPath(root string, cfg Config, now time.Time, taskID string) (string, error) {
	if !cfg.EnableRuns {
		return "", nil
	}
	if err := ensureDir(runsDir(root)); err != nil {
		return "", err
	}
	return filepath.Join(runsDir(root), fmt.Sprintf("%s_%s.log", now.Format("20060102T150405"), taskID)), nil
}

func runAgentCommandMode(ctx context.Context, root string, cfg Config, taskID string, now time.Time, mode string, runLogPath string) (exit int, logPath string, err error) {
	cmdLine := strings.TrimSpace(agentCommandForMode(cfg, mode))
	if cmdLine == "" {
		return 0, runLogPath, fmt.Errorf("agent command is not configured for mode %s", mode)
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdLine)
	td := taskDir(root, taskID)
	repoRoot := resolveRepoRoot(root)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"HAZEL_ROOT="+repoRoot,
		"HAZEL_STATE_ROOT="+root,
		"HAZEL_REPO_ROOT="+repoRoot,
		"HAZEL_TASK_ID="+taskID,
		"HAZEL_TASK_DIR="+td,
		"HAZEL_AGENT_PACKET="+taskFile(root, taskID, "agent_packet.md"),
		"HAZEL_PROMPT_PACKET="+taskFile(root, taskID, "prompt_packet.md"),
		"HAZEL_MODE="+mode,
	)

	var out bytes.Buffer
	var lw io.Writer = &out
	var f *os.File
	if runLogPath != "" {
		ff, ferr := os.OpenFile(runLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if ferr != nil {
			return 0, "", ferr
		}
		f = ff
		// Stream logs to disk for UI tailing; avoid buffering potentially huge output in memory.
		lw = f
	}
	cmd.Stdout = lw
	cmd.Stderr = lw

	err = cmd.Run()
	exit = 0
	if err != nil {
		if ee := (*exec.ExitError)(nil); errorAs(err, &ee) {
			exit = ee.ExitCode()
		} else {
			return 0, runLogPath, err
		}
	}

	if f != nil {
		_ = f.Close()
	}
	return exit, runLogPath, nil
}

func errorAs(err error, target any) bool {
	return errorsAs(err, target)
}

func agentCommandForMode(cfg Config, mode string) string {
	switch mode {
	case "plan":
		if strings.TrimSpace(cfg.AgentPlanCommand) != "" {
			return cfg.AgentPlanCommand
		}
	case "implement":
		if strings.TrimSpace(cfg.AgentImplementCommand) != "" {
			return cfg.AgentImplementCommand
		}
	}
	return strings.TrimSpace(cfg.AgentCommand)
}

func selectNextReadyFromFS(root string, tasks []*BoardTask) *BoardTask {
	var ready []*BoardTask
	for _, t := range tasks {
		if t.Status == StatusReady {
			ready = append(ready, t)
		}
	}
	if len(ready) == 0 {
		return nil
	}

	priorityRank := func(t *BoardTask) (rank int, has bool) {
		// Priority comes from task.md hazel config.
		if md, err := readTaskMD(root, t.ID); err == nil {
			if lbl, ok := getTaskPriorityFromMD(md); ok {
				switch lbl {
				case "HIGH":
					return 0, true
				case "MEDIUM":
					return 1, true
				case "LOW":
					return 2, true
				}
			}
		}
		return 0, false
	}

	sort.SliceStable(ready, func(i, j int) bool {
		a, b := ready[i], ready[j]
		ap, ah := priorityRank(a)
		bp, bh := priorityRank(b)
		if !ah && bh {
			return false
		}
		if ah && !bh {
			return true
		}
		if ah && bh && ap != bp {
			return ap < bp
		}
		if a.Order == nil && b.Order != nil {
			return false
		}
		if a.Order != nil && b.Order == nil {
			return true
		}
		if a.Order != nil && b.Order != nil && *a.Order != *b.Order {
			return *a.Order < *b.Order
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	return ready[0]
}
