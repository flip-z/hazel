package hazel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type ChatMessage struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	TaskID    string    `json:"task_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ChatLog struct {
	Messages []ChatMessage `json:"messages"`
}

func chatLogPath(root string) string {
	return filepath.Join(hazelDir(root), "chat", "messages.json")
}

func readChatLog(root string) (*ChatLog, error) {
	b, err := os.ReadFile(chatLogPath(root))
	if err != nil {
		return &ChatLog{}, nil
	}
	var log ChatLog
	if err := json.Unmarshal(b, &log); err != nil {
		return &ChatLog{}, nil
	}
	return &log, nil
}

func appendChatMessage(root string, msg ChatMessage) error {
	log, _ := readChatLog(root)
	log.Messages = append(log.Messages, msg)
	if len(log.Messages) > 200 {
		log.Messages = log.Messages[len(log.Messages)-200:]
	}
	b, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeFileAtomic(chatLogPath(root), b, 0o644)
}

func runChatPrompt(ctx context.Context, root string, taskID string, prompt string) (string, error) {
	cfg, err := loadConfigOrDefault(root)
	if err != nil {
		return "", err
	}
	repoRoot := resolveRepoRoot(root)
	mode := "chat"

	now := time.Now()
	contextPacket := buildChatPromptPacket(root, taskID, prompt, now)
	promptPath := filepath.Join(hazelDir(root), "chat", fmt.Sprintf("%s_prompt.md", now.Format("20060102T150405")))
	if err := writeFileAtomic(promptPath, []byte(contextPacket), 0o644); err != nil {
		return "", err
	}

	cmdLine := strings.TrimSpace(cfg.AgentChatCommand)
	if cmdLine == "" {
		cmdLine = strings.TrimSpace(cfg.AgentImplementCommand)
	}
	if cmdLine == "" {
		cmdLine = strings.TrimSpace(cfg.AgentCommand)
	}
	if cmdLine == "" {
		cmdLine = `codex exec "$(cat \"$HAZEL_CHAT_PROMPT_FILE\")"`
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", cmdLine)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"HAZEL_MODE="+mode,
		"HAZEL_ROOT="+repoRoot,
		"HAZEL_STATE_ROOT="+root,
		"HAZEL_REPO_ROOT="+repoRoot,
		"HAZEL_TASK_ID="+taskID,
		"HAZEL_CHAT_PROMPT="+prompt,
		"HAZEL_CHAT_PROMPT_FILE="+promptPath,
	)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		if ee := (*exec.ExitError)(nil); errorAs(err, &ee) {
			return out.String(), fmt.Errorf("chat command failed with exit code %d", ee.ExitCode())
		}
		return out.String(), err
	}
	resp := strings.TrimSpace(out.String())
	if resp == "" {
		resp = "(no output)"
	}
	return resp, nil
}

func buildChatPromptPacket(root string, taskID string, prompt string, now time.Time) string {
	repoRoot := resolveRepoRoot(root)
	var taskContext string
	if strings.TrimSpace(taskID) != "" {
		var b Board
		if err := readYAMLFile(boardPath(root), &b); err == nil {
			for _, t := range b.Tasks {
				if t.ID == taskID {
					body, err := buildPromptPacket(root, t, "chat", now)
					if err == nil {
						taskContext = body
					}
					break
				}
			}
		}
	}

	wikiReadme, _ := os.ReadFile(filepath.Join(root, "wiki", "README.md"))
	wikiFeatures, _ := os.ReadFile(filepath.Join(root, "wiki", "FEATURES_AND_USAGE.md"))
	wikiChangelog, _ := os.ReadFile(filepath.Join(root, "wiki", "CHANGELOG.md"))
	agentsMD, _ := os.ReadFile(filepath.Join(repoRoot, "AGENTS.md"))

	var sb strings.Builder
	sb.WriteString("# Hazel Chat Packet\n\n")
	sb.WriteString("- Generated: `" + now.Format(time.RFC3339) + "`\n")
	sb.WriteString("- State root: `" + root + "`\n")
	sb.WriteString("- Repo root: `" + repoRoot + "`\n")
	if taskID != "" {
		sb.WriteString("- Focus task: `" + taskID + "`\n")
	}
	sb.WriteString("\n## User Prompt\n\n")
	sb.WriteString("```text\n" + clipped(prompt, 8000) + "\n```\n")

	if strings.TrimSpace(taskContext) != "" {
		sb.WriteString("\n## Task Context\n\n")
		sb.WriteString(taskContext)
	}
	if strings.TrimSpace(string(agentsMD)) != "" {
		sb.WriteString("\n## AGENTS.md (trimmed)\n\n")
		sb.WriteString("```markdown\n" + clipped(string(agentsMD), 3000) + "\n```\n")
	}
	if strings.TrimSpace(string(wikiReadme)) != "" {
		sb.WriteString("\n## Wiki README (trimmed)\n\n")
		sb.WriteString("```markdown\n" + clipped(string(wikiReadme), 2200) + "\n```\n")
	}
	if strings.TrimSpace(string(wikiFeatures)) != "" {
		sb.WriteString("\n## Wiki Features/Usage (trimmed)\n\n")
		sb.WriteString("```markdown\n" + clipped(string(wikiFeatures), 2200) + "\n```\n")
	}
	if strings.TrimSpace(string(wikiChangelog)) != "" {
		sb.WriteString("\n## Wiki Changelog (trimmed)\n\n")
		sb.WriteString("```markdown\n" + clipped(string(wikiChangelog), 1800) + "\n```\n")
	}

	return sb.String()
}
