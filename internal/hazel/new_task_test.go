package hazel

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateNewTaskAllocatesIDAndScaffold(t *testing.T) {
	root := t.TempDir()
	if err := InitRepo(nil, root, InitOptions{}); err != nil {
		t.Fatalf("init repo: %v", err)
	}

	// Seed existing ids to ensure incrementing.
	b := &Board{Version: 1, Tasks: []*BoardTask{
		{ID: "HZ-0007", Title: "x", Status: StatusBacklog, CreatedAt: mustTime(t), UpdatedAt: mustTime(t)},
	}}
	if err := writeYAMLFile(boardPath(root), b); err != nil {
		t.Fatalf("write board: %v", err)
	}

	tk, err := createNewTask(root, "hello")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if tk.ID != "HZ-0008" {
		t.Fatalf("expected HZ-0008, got %s", tk.ID)
	}
	if _, err := os.Stat(filepath.Join(tasksDir(root), tk.ID, "task.md")); err != nil {
		t.Fatalf("missing scaffold: %v", err)
	}
}

func mustTime(t *testing.T) (tt time.Time) {
	t.Helper()
	return time.Date(2026, 2, 9, 12, 0, 0, 0, time.Local)
}
