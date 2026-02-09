package hazel

import (
	"context"
	"fmt"
	"path/filepath"
)

type DoctorReport struct {
	Problems []string
	Warnings []string
}

func Doctor(ctx context.Context, root string) (*DoctorReport, error) {
	_ = ctx
	r := &DoctorReport{}

	for _, p := range []string{hazelDir(root), boardPath(root), configPath(root), tasksDir(root)} {
		if !exists(p) {
			r.Problems = append(r.Problems, fmt.Sprintf("missing %s", rel(root, p)))
		}
	}
	if len(r.Problems) != 0 {
		return r, nil
	}

	var b Board
	if err := readYAMLFile(boardPath(root), &b); err != nil {
		r.Problems = append(r.Problems, err.Error())
		return r, nil
	}
	if err := b.Validate(); err != nil {
		r.Problems = append(r.Problems, err.Error())
		return r, nil
	}

	var cfg Config
	if err := readYAMLFile(configPath(root), &cfg); err != nil {
		r.Problems = append(r.Problems, err.Error())
		return r, nil
	}
	if cfg.Port == 0 {
		r.Warnings = append(r.Warnings, "config port is 0")
	}
	if cfg.RunIntervalSeconds < 0 {
		r.Problems = append(r.Problems, "run_interval_seconds must be >= 0")
	}

	for _, t := range b.Tasks {
		td := taskDir(root, t.ID)
		if !exists(td) {
			r.Warnings = append(r.Warnings, fmt.Sprintf("missing task directory %s", rel(root, td)))
			continue
		}
		for _, f := range []string{"task.md", "impl.md"} {
			p := filepath.Join(td, f)
			if !exists(p) {
				r.Warnings = append(r.Warnings, fmt.Sprintf("%s missing %s", t.ID, rel(root, p)))
			}
		}
	}

	return r, nil
}

func rel(root, p string) string {
	if rp, err := filepath.Rel(root, p); err == nil {
		return rp
	}
	return p
}
