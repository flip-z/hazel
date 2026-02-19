package hazel

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type ProjectMeta struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	RepoPath string `json:"repo_path"`
	RepoSlug string `json:"repo_slug,omitempty"`
}

func projectMetaPath(stateRoot string) string {
	return filepath.Join(hazelDir(stateRoot), "project.json")
}

func writeProjectMeta(stateRoot string, m ProjectMeta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeFileAtomic(projectMetaPath(stateRoot), b, 0o644)
}

func readProjectMeta(stateRoot string) (*ProjectMeta, error) {
	b, err := os.ReadFile(projectMetaPath(stateRoot))
	if err != nil {
		return nil, err
	}
	var m ProjectMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func resolveRepoRoot(stateRoot string) string {
	m, err := readProjectMeta(stateRoot)
	if err == nil && m != nil && m.RepoPath != "" {
		return m.RepoPath
	}
	return stateRoot
}
