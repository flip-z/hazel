package hazel

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	var cfg Config
	if err := readYAMLFile(configPath(root), &cfg); err != nil {
		return nil, err
	}
	if cfg.Version == 0 {
		cfg = defaultConfig()
	}
	if cfg.AgentCommand == "" && !opt.DryRun {
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
	}

	res := &RunResult{DispatchedTaskID: next.ID}

	if opt.DryRun {
		return res, nil
	}

	exit, logPath, err := runAgentCommand(ctx, root, cfg, next.ID, now)
	if err != nil {
		// Still reconcile state below.
		exit = 1
	}
	res.AgentExitCode = &exit
	res.RunLogPath = logPath

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
	if err := ensureDir(filepath.Join(td, "artifacts")); err != nil {
		return err
	}
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
`,
		t.ID, t.Title, t.Status, now.Format(time.RFC3339),
		rel(root, taskFile(root, t.ID, "task.md")),
		rel(root, taskFile(root, t.ID, "impl.md")),
	)
	return writeFileAtomic(p, []byte(body), 0o644)
}

func runAgentCommand(ctx context.Context, root string, cfg Config, taskID string, now time.Time) (exit int, logPath string, err error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", cfg.AgentCommand)
	td := taskDir(root, taskID)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"HAZEL_ROOT="+root,
		"HAZEL_TASK_ID="+taskID,
		"HAZEL_TASK_DIR="+td,
		"HAZEL_AGENT_PACKET="+taskFile(root, taskID, "agent_packet.md"),
	)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	runLogPath := ""
	if cfg.EnableRuns {
		if err := ensureDir(runsDir(root)); err != nil {
			return 0, "", err
		}
		runLogPath = filepath.Join(runsDir(root), fmt.Sprintf("%s_%s.log", now.Format("20060102T150405"), taskID))
	}

	err = cmd.Run()
	exit = 0
	if err != nil {
		if ee := (*exec.ExitError)(nil); errorAs(err, &ee) {
			exit = ee.ExitCode()
		} else {
			return 0, runLogPath, err
		}
	}

	if cfg.EnableRuns && runLogPath != "" {
		if werr := writeFileAtomic(runLogPath, out.Bytes(), 0o644); werr != nil {
			return exit, runLogPath, werr
		}
	}
	return exit, runLogPath, nil
}

func errorAs(err error, target any) bool {
	return errorsAs(err, target)
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
