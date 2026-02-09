package hazel

import (
	"path/filepath"
)

func hazelDir(root string) string {
	return filepath.Join(root, ".hazel")
}

func boardPath(root string) string { return filepath.Join(hazelDir(root), "board.yaml") }
func configPath(root string) string {
	return filepath.Join(hazelDir(root), "config.yaml")
}
func tasksDir(root string) string   { return filepath.Join(hazelDir(root), "tasks") }
func runsDir(root string) string    { return filepath.Join(hazelDir(root), "runs") }
func archiveDir(root string) string { return filepath.Join(hazelDir(root), "archive") }
func serverStatePath(root string) string {
	return filepath.Join(hazelDir(root), "server.json")
}

func taskDir(root, id string) string {
	return filepath.Join(tasksDir(root), id)
}

func taskFile(root, id, name string) string {
	return filepath.Join(taskDir(root, id), name)
}
