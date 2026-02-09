package hazel

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var taskIDRe = regexp.MustCompile(`^HZ-(\d{4,})$`)

func createNewTask(root string, title string) (*BoardTask, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}

	var b Board
	if err := readYAMLFile(boardPath(root), &b); err != nil {
		return nil, err
	}
	if b.Version == 0 {
		b.Version = 1
	}

	nextID, err := nextTaskID(root, &b)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	t := &BoardTask{
		ID:        nextID,
		Title:     title,
		Status:    StatusBacklog,
		CreatedAt: now,
		UpdatedAt: now,
	}
	b.Tasks = append(b.Tasks, t)

	if err := writeYAMLFile(boardPath(root), &b); err != nil {
		return nil, err
	}
	if err := ensureTaskScaffoldWithColor(root, t.ID, randomColorKey()); err != nil {
		return nil, err
	}
	return t, nil
}

func nextTaskID(root string, b *Board) (string, error) {
	max := 0
	seen := map[string]bool{}
	for _, t := range b.Tasks {
		seen[t.ID] = true
		if n := parseTaskNum(t.ID); n > max {
			max = n
		}
	}

	// Also consider directories, so we don't collide with tasks that exist on disk but aren't on the board.
	paths := []string{tasksDir(root), archiveDir(root)}
	for _, base := range paths {
		ents, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			id := e.Name()
			seen[id] = true
			if n := parseTaskNum(id); n > max {
				max = n
			}
		}
	}

	// Generate next available id, skipping collisions.
	for i := max + 1; i < max+100000; i++ {
		id := fmt.Sprintf("HZ-%04d", i)
		if !seen[id] {
			// Avoid collision with existing files if someone created a weird symlink etc.
			if _, err := os.Stat(filepath.Join(tasksDir(root), id)); err == nil {
				continue
			}
			if _, err := os.Stat(filepath.Join(archiveDir(root), id)); err == nil {
				continue
			}
			return id, nil
		}
	}
	return "", fmt.Errorf("unable to allocate next task id")
}

func parseTaskNum(id string) int {
	m := taskIDRe.FindStringSubmatch(id)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

func sortTasksByID(tasks []*BoardTask) {
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
}
