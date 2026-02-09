package hazel

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type Status string

const (
	StatusBacklog Status = "BACKLOG"
	StatusReady   Status = "READY"
	StatusActive  Status = "ACTIVE"
	StatusReview  Status = "REVIEW"
	StatusDone    Status = "DONE"
)

func (s Status) Valid() bool {
	switch s {
	case StatusBacklog, StatusReady, StatusActive, StatusReview, StatusDone:
		return true
	default:
		return false
	}
}

type Board struct {
	Version int          `yaml:"version"`
	Tasks   []*BoardTask `yaml:"tasks"`
}

type BoardTask struct {
	ID        string    `yaml:"id"`
	Title     string    `yaml:"title"`
	Status    Status    `yaml:"status"`
	Order     *int      `yaml:"order,omitempty"`
	CreatedAt time.Time `yaml:"created_at"`
	UpdatedAt time.Time `yaml:"updated_at"`
	Deps      []string  `yaml:"deps,omitempty"`
}

func (t *BoardTask) Validate() error {
	if strings.TrimSpace(t.ID) == "" {
		return errors.New("task id is required")
	}
	if strings.TrimSpace(t.Title) == "" {
		return fmt.Errorf("%s: title is required", t.ID)
	}
	if !t.Status.Valid() {
		return fmt.Errorf("%s: invalid status %q", t.ID, t.Status)
	}
	if t.CreatedAt.IsZero() {
		return fmt.Errorf("%s: created_at is required", t.ID)
	}
	if t.UpdatedAt.IsZero() {
		return fmt.Errorf("%s: updated_at is required", t.ID)
	}
	return nil
}

func (b *Board) Validate() error {
	if b.Version == 0 {
		b.Version = 1
	}
	seen := map[string]bool{}
	for _, t := range b.Tasks {
		if t == nil {
			return errors.New("board contains null task")
		}
		if err := t.Validate(); err != nil {
			return err
		}
		if seen[t.ID] {
			return fmt.Errorf("duplicate task id %s", t.ID)
		}
		seen[t.ID] = true
	}
	return nil
}

type Config struct {
	Version             int    `yaml:"version"`
	Port                int    `yaml:"port"`
	RunIntervalSeconds  int    `yaml:"run_interval_seconds"`
	SchedulerEnabled    bool   `yaml:"scheduler_enabled"`
	AgentCommand        string `yaml:"agent_command"`
	EnableEnrichment    bool   `yaml:"enable_enrichment"`
	EnableRuns          bool   `yaml:"enable_runs"`
	UIHideDoneByDefault bool   `yaml:"ui_hide_done_by_default"`
}

func defaultConfig() Config {
	return Config{
		Version:             1,
		Port:                8765,
		RunIntervalSeconds:  60,
		SchedulerEnabled:    false,
		AgentCommand:        "",
		EnableEnrichment:    false,
		EnableRuns:          true,
		UIHideDoneByDefault: true,
	}
}
