package hazel

import (
	"os"
	"path/filepath"
	"strings"
)

func projectTitle(root string) string {
	// Use cwd folder name as a stable default.
	base := filepath.Base(root)
	if base != "" && base != "." && base != string(filepath.Separator) {
		return base
	}
	return "project"
}

func hazelPoweredByURL() string {
	return "https://github.com/flip-z/hazel"
}

func readRepoSlugFromGitConfig(root string) string {
	// Best-effort: parse .git/config for an origin URL like:
	//   https://github.com/flip-z/hazel.git
	//   git@github.com:flip-z/hazel.git
	b, err := os.ReadFile(filepath.Join(root, ".git", "config"))
	if err != nil {
		return ""
	}
	s := string(b)
	i := strings.Index(s, "[remote \"origin\"]")
	if i < 0 {
		return ""
	}
	s = s[i:]
	// Find url = ...
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "url = ") {
			u := strings.TrimSpace(strings.TrimPrefix(line, "url = "))
			u = strings.TrimSuffix(u, ".git")
			// https://github.com/OWNER/REPO
			if strings.Contains(u, "github.com/") {
				parts := strings.Split(u, "github.com/")
				if len(parts) >= 2 {
					path := parts[len(parts)-1]
					path = strings.TrimPrefix(path, ":")
					path = strings.TrimPrefix(path, "/")
					segs := strings.Split(path, "/")
					if len(segs) >= 2 {
						return segs[0] + "/" + segs[1]
					}
				}
			}
			// git@github.com:OWNER/REPO
			if strings.Contains(u, "github.com:") {
				parts := strings.Split(u, "github.com:")
				if len(parts) >= 2 {
					path := parts[len(parts)-1]
					path = strings.TrimPrefix(path, "/")
					segs := strings.Split(path, "/")
					if len(segs) >= 2 {
						return segs[0] + "/" + segs[1]
					}
				}
			}
		}
	}
	return ""
}

