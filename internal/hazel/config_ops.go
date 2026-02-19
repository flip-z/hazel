package hazel

import "strings"

type ConfigUpdate struct {
	GitHubToken      *string
	GitBaseBranch    *string
	ClearGitHubToken bool
}

func UpdateConfig(root string, upd ConfigUpdate) error {
	cfg, _ := loadConfigOrDefault(root)
	if upd.ClearGitHubToken {
		cfg.GitHubToken = ""
	}
	if upd.GitHubToken != nil {
		cfg.GitHubToken = strings.TrimSpace(*upd.GitHubToken)
	}
	if upd.GitBaseBranch != nil {
		cfg.GitBaseBranch = strings.TrimSpace(*upd.GitBaseBranch)
	}
	if strings.TrimSpace(cfg.GitBaseBranch) == "" {
		cfg.GitBaseBranch = "main"
	}
	return writeYAMLFile(configPath(root), &cfg)
}
