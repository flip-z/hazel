package hazel

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestArchiveDoneMovesTasksAndUpdatesBoard(t *testing.T) {
	root := t.TempDir()
	if err := InitRepo(nil, root, InitOptions{}); err != nil { // ctx unused
		t.Fatalf("init repo: %v", err)
	}

	now := time.Date(2026, 2, 9, 12, 0, 0, 0, time.Local)
	done := &BoardTask{ID: "HZ-0001", Title: "done", Status: StatusDone, CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-24 * time.Hour)}
	ready := &BoardTask{ID: "HZ-0002", Title: "ready", Status: StatusReady, CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-24 * time.Hour)}
	b := &Board{Version: 1, Tasks: []*BoardTask{done, ready}}
	if err := writeYAMLFile(boardPath(root), b); err != nil {
		t.Fatalf("write board: %v", err)
	}
	if err := ensureTaskScaffold(root, "HZ-0001"); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if err := ensureTaskScaffold(root, "HZ-0002"); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	res, err := ArchiveDone(nil, root, ArchiveOptions{})
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if len(res.ArchivedIDs) != 1 || res.ArchivedIDs[0] != "HZ-0001" {
		t.Fatalf("unexpected archived ids: %#v", res.ArchivedIDs)
	}

	if _, err := os.Stat(filepath.Join(archiveDir(root), "HZ-0001")); err != nil {
		t.Fatalf("archived dir missing: %v", err)
	}
	if _, err := os.Stat(taskDir(root, "HZ-0001")); !os.IsNotExist(err) {
		t.Fatalf("expected original task dir removed, got err=%v", err)
	}

	var b2 Board
	if err := readYAMLFile(boardPath(root), &b2); err != nil {
		t.Fatalf("read board: %v", err)
	}
	if len(b2.Tasks) != 1 || b2.Tasks[0].ID != "HZ-0002" {
		t.Fatalf("unexpected board tasks: %#v", b2.Tasks)
	}
}

func TestArchiveDoneBeforeFilter(t *testing.T) {
	root := t.TempDir()
	if err := InitRepo(nil, root, InitOptions{}); err != nil {
		t.Fatalf("init repo: %v", err)
	}

	now := time.Date(2026, 2, 9, 12, 0, 0, 0, time.Local)
	oldDone := &BoardTask{ID: "HZ-0001", Title: "done old", Status: StatusDone, CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-72 * time.Hour)}
	newDone := &BoardTask{ID: "HZ-0002", Title: "done new", Status: StatusDone, CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-24 * time.Hour)}
	b := &Board{Version: 1, Tasks: []*BoardTask{oldDone, newDone}}
	if err := writeYAMLFile(boardPath(root), b); err != nil {
		t.Fatalf("write board: %v", err)
	}
	_ = ensureTaskScaffold(root, "HZ-0001")
	_ = ensureTaskScaffold(root, "HZ-0002")

	before := now.Add(-36 * time.Hour)
	res, err := ArchiveDone(nil, root, ArchiveOptions{Before: &before})
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if len(res.ArchivedIDs) != 1 || res.ArchivedIDs[0] != "HZ-0001" {
		t.Fatalf("unexpected archived ids: %#v", res.ArchivedIDs)
	}
}
