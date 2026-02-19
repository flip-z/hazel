package hazel

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func gitBaseBranch(cfg Config) string {
	base := strings.TrimSpace(cfg.GitBaseBranch)
	if base == "" {
		return "main"
	}
	return base
}

func taskBranchName(taskID, title string) string {
	slug := strings.ToLower(strings.TrimSpace(title))
	var b strings.Builder
	lastDash := false
	for _, r := range slug {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "task"
	}
	return "task/" + strings.ToLower(strings.TrimSpace(taskID)) + "-" + s
}

func runCmd(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s %s failed: %s", name, strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

func captureTaskGitMeta(project TrackedProject, task *BoardTask, cfg Config) (taskGitMeta, error) {
	md, err := readTaskMD(project.StorageRoot, task.ID)
	if err != nil {
		return taskGitMeta{}, err
	}
	if g, ok := getTaskGitFromMD(md); ok {
		return g, nil
	}
	return taskGitMeta{}, nil
}

func saveTaskGitMeta(project TrackedProject, taskID string, update func(*taskGitMeta)) error {
	md, err := readTaskMD(project.StorageRoot, taskID)
	if err != nil {
		return err
	}
	updated, err := setTaskGitInMD(md, update)
	if err != nil {
		return err
	}
	if err := writeTaskMD(project.StorageRoot, taskID, updated); err != nil {
		return err
	}
	return bumpBoardUpdatedAt(project.StorageRoot, taskID)
}

func startTaskBranch(project TrackedProject, task *BoardTask, cfg Config) (taskGitMeta, error) {
	base := gitBaseBranch(cfg)
	branch := taskBranchName(task.ID, task.Title)
	if _, err := runCmd(project.RepoPath, nil, "git", "checkout", base); err != nil {
		return taskGitMeta{}, err
	}
	_, _ = runCmd(project.RepoPath, nil, "git", "pull", "--ff-only", "origin", base)
	if _, err := runCmd(project.RepoPath, nil, "git", "checkout", "-B", branch); err != nil {
		return taskGitMeta{}, err
	}
	meta := taskGitMeta{
		Branch:     branch,
		Base:       base,
		LastCommit: "",
		PRURL:      "",
		MergeSHA:   "",
		MergedAt:   "",
	}
	if err := saveTaskGitMeta(project, task.ID, func(g *taskGitMeta) {
		*g = meta
	}); err != nil {
		return taskGitMeta{}, err
	}
	return meta, nil
}

func commitTaskChanges(project TrackedProject, task *BoardTask, msg string) (string, error) {
	if _, err := runCmd(project.RepoPath, nil, "git", "add", "-A"); err != nil {
		return "", err
	}
	if _, err := runCmd(project.RepoPath, nil, "git", "commit", "-m", msg); err != nil {
		return "", err
	}
	sha, err := runCmd(project.RepoPath, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if err := saveTaskGitMeta(project, task.ID, func(g *taskGitMeta) {
		g.LastCommit = strings.TrimSpace(sha)
	}); err != nil {
		return "", err
	}
	return sha, nil
}

func openTaskPR(project TrackedProject, task *BoardTask, cfg Config, meta taskGitMeta) (string, error) {
	branch := strings.TrimSpace(meta.Branch)
	if branch == "" {
		branch = taskBranchName(task.ID, task.Title)
	}
	base := strings.TrimSpace(meta.Base)
	if base == "" {
		base = gitBaseBranch(cfg)
	}
	if _, err := runCmd(project.RepoPath, nil, "git", "push", "-u", "origin", branch); err != nil {
		return "", err
	}
	title := task.ID + ": " + task.Title
	body := "Created by Hazel for task " + task.ID
	ghArgs := []string{"pr", "create", "--base", base, "--head", branch, "--title", title, "--body", body}
	if strings.TrimSpace(project.RepoSlug) != "" {
		ghArgs = append(ghArgs, "--repo", strings.TrimSpace(project.RepoSlug))
	}
	env := []string{}
	if tok := strings.TrimSpace(cfg.GitHubToken); tok != "" {
		env = append(env, "GH_TOKEN="+tok)
	}
	out, err := runCmd(project.RepoPath, env, "gh", ghArgs...)
	if err != nil {
		return "", err
	}
	prURL := strings.TrimSpace(strings.Fields(out)[0])
	if !strings.HasPrefix(prURL, "http://") && !strings.HasPrefix(prURL, "https://") {
		return "", fmt.Errorf("unable to parse PR URL from gh output: %q", out)
	}
	if err := saveTaskGitMeta(project, task.ID, func(g *taskGitMeta) {
		g.Branch = branch
		g.Base = base
		g.PRURL = prURL
	}); err != nil {
		return "", err
	}
	return prURL, nil
}

func markTaskMerged(project TrackedProject, task *BoardTask, mergeSHA string) error {
	sha := strings.TrimSpace(mergeSHA)
	if sha == "" {
		return fmt.Errorf("merge sha is required")
	}
	return saveTaskGitMeta(project, task.ID, func(g *taskGitMeta) {
		g.MergeSHA = sha
		g.MergedAt = time.Now().UTC().Format(time.RFC3339)
	})
}
