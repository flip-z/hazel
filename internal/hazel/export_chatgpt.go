package hazel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ExportChatGPTBundle(ctx context.Context, root string, outDir string) error {
	_ = ctx
	if err := ensureDir(outDir); err != nil {
		return err
	}

	nx, _ := LoadNexus(root)
	if nx != nil {
		for _, p := range nx.Projects {
			pdir := filepath.Join(outDir, p.Key)
			if err := ensureDir(pdir); err != nil {
				return err
			}
			if err := exportChatGPTProject(p.StorageRoot, p.Name, p.RepoPath, pdir); err != nil {
				return err
			}
		}
		return nil
	}

	return exportChatGPTProject(root, projectTitle(root), root, outDir)
}

func exportChatGPTProject(stateRoot string, title string, repoPath string, outDir string) error {
	var b Board
	if err := readYAMLFile(boardPath(stateRoot), &b); err != nil {
		return err
	}
	if err := b.Validate(); err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString("# " + title + " - Hazel Context\n\n")
	sb.WriteString("- Repo path: `" + repoPath + "`\n")
	sb.WriteString("- State root: `" + stateRoot + "`\n\n")
	sb.WriteString("## Board\n\n")
	for _, t := range b.Tasks {
		sb.WriteString(fmt.Sprintf("- %s | %s | %s\n", t.ID, t.Status, t.Title))
	}
	if err := writeFileAtomic(filepath.Join(outDir, "HAZEL_CONTEXT.md"), []byte(sb.String()), 0o644); err != nil {
		return err
	}

	copyIf := func(src string, dst string) error {
		b, err := os.ReadFile(src)
		if err != nil {
			return nil
		}
		return writeFileAtomic(dst, b, 0o644)
	}
	_ = copyIf(filepath.Join(stateRoot, "wiki", "README.md"), filepath.Join(outDir, "WIKI_README.md"))
	_ = copyIf(filepath.Join(stateRoot, "wiki", "FEATURES_AND_USAGE.md"), filepath.Join(outDir, "WIKI_FEATURES_AND_USAGE.md"))
	_ = copyIf(filepath.Join(stateRoot, "wiki", "CHANGELOG.md"), filepath.Join(outDir, "WIKI_CHANGELOG.md"))
	_ = copyIf(filepath.Join(stateRoot, "wiki", "SOURCE_README.md"), filepath.Join(outDir, "SOURCE_README.md"))
	_ = copyIf(filepath.Join(resolveRepoRoot(stateRoot), "AGENTS.md"), filepath.Join(outDir, "AGENTS.md"))

	return nil
}
