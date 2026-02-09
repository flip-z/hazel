package hazel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type ArchiveOptions struct {
	Before *time.Time
	DryRun bool
}

type ArchiveResult struct {
	ArchivedIDs []string
}

func ArchiveDone(ctx context.Context, root string, opt ArchiveOptions) (*ArchiveResult, error) {
	_ = ctx
	var b Board
	if err := readYAMLFile(boardPath(root), &b); err != nil {
		return nil, err
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}

	var keep []*BoardTask
	var archived []string
	for _, t := range b.Tasks {
		if t.Status != StatusDone {
			keep = append(keep, t)
			continue
		}
		if opt.Before != nil && !t.UpdatedAt.Before(*opt.Before) {
			keep = append(keep, t)
			continue
		}
		archived = append(archived, t.ID)
		if !opt.DryRun {
			src := taskDir(root, t.ID)
			dst := filepath.Join(archiveDir(root), t.ID)
			if exists(src) {
				if err := ensureDir(archiveDir(root)); err != nil {
					return nil, err
				}
				_ = os.RemoveAll(dst)
				if err := os.Rename(src, dst); err != nil {
					return nil, fmt.Errorf("archive %s: %w", t.ID, err)
				}
			}
		}
	}
	b.Tasks = keep
	if !opt.DryRun {
		if err := writeYAMLFile(boardPath(root), &b); err != nil {
			return nil, err
		}
	}
	return &ArchiveResult{ArchivedIDs: archived}, nil
}
