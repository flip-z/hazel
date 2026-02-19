package hazel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func writePromptPacket(root string, t *BoardTask, mode string, now time.Time) (string, error) {
	body, err := buildPromptPacket(root, t, mode, now)
	if err != nil {
		return "", err
	}
	p := taskFile(root, t.ID, "prompt_packet.md")
	if err := writeFileAtomic(p, []byte(body), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

func buildPromptPacket(root string, t *BoardTask, mode string, now time.Time) (string, error) {
	repoRoot := resolveRepoRoot(root)
	taskMD, _ := readTaskMD(root, t.ID)
	if stripped, err := stripTaskConfigForRender(taskMD); err == nil {
		taskMD = stripped
	}
	implMD, _ := os.ReadFile(taskFile(root, t.ID, "impl.md"))

	agentsMD, _ := os.ReadFile(filepath.Join(repoRoot, "AGENTS.md"))
	readmeMD, _ := os.ReadFile(filepath.Join(root, "wiki", "SOURCE_README.md"))
	featuresMD, _ := os.ReadFile(filepath.Join(root, "wiki", "FEATURES_AND_USAGE.md"))
	changelogMD, _ := os.ReadFile(filepath.Join(root, "wiki", "CHANGELOG.md"))

	var sb strings.Builder
	sb.WriteString("# Hazel Prompt Packet\n\n")
	sb.WriteString(fmt.Sprintf("- Mode: `%s`\n", mode))
	sb.WriteString(fmt.Sprintf("- Generated: `%s`\n", now.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("- Task: `%s`\n", t.ID))
	sb.WriteString(fmt.Sprintf("- Title: %s\n", t.Title))
	sb.WriteString(fmt.Sprintf("- Status: `%s`\n", t.Status))
	sb.WriteString(fmt.Sprintf("- State root: `%s`\n", root))
	sb.WriteString(fmt.Sprintf("- Repo root: `%s`\n", repoRoot))
	sb.WriteString("\n## Instructions\n\n")
	sb.WriteString("- Follow AGENTS.md constraints.\n")
	sb.WriteString("- Do not edit human intent in task.md unless explicitly requested.\n")
	if mode == "plan" {
		sb.WriteString("- In plan mode, write the proposal to plan.md (temporary, pending accept/decline).\n")
	} else {
		sb.WriteString("- Keep implementation notes in impl.md.\n")
	}
	sb.WriteString("\n## Task (trimmed)\n\n")
	sb.WriteString("```markdown\n" + clipped(taskMD, 6000) + "\n```\n")
	sb.WriteString("\n## Implementation Notes (trimmed)\n\n")
	sb.WriteString("```markdown\n" + clipped(string(implMD), 3000) + "\n```\n")

	if strings.TrimSpace(string(agentsMD)) != "" {
		sb.WriteString("\n## AGENTS.md (trimmed)\n\n")
		sb.WriteString("```markdown\n" + clipped(string(agentsMD), 3000) + "\n```\n")
	}
	if strings.TrimSpace(string(readmeMD)) != "" {
		sb.WriteString("\n## Source README (trimmed)\n\n")
		sb.WriteString("```markdown\n" + clipped(string(readmeMD), 3000) + "\n```\n")
	}
	if strings.TrimSpace(string(featuresMD)) != "" {
		sb.WriteString("\n## Features/Usage Wiki (trimmed)\n\n")
		sb.WriteString("```markdown\n" + clipped(string(featuresMD), 3000) + "\n```\n")
	}
	if strings.TrimSpace(string(changelogMD)) != "" {
		sb.WriteString("\n## Changelog (trimmed)\n\n")
		sb.WriteString("```markdown\n" + clipped(string(changelogMD), 2200) + "\n```\n")
	}

	return sb.String(), nil
}

func clipped(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	if max < 32 {
		return s[:max]
	}
	return s[:max] + "\n\n[...truncated by Hazel...]"
}

func summarizeJSONEventsFromLog(logPath string) map[string]any {
	if strings.TrimSpace(logPath) == "" {
		return nil
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(b), "\n")
	total := 0
	types := map[string]int{}
	lastText := ""
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || !strings.HasPrefix(ln, "{") {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			continue
		}
		total++
		typ := "unknown"
		if v, ok := m["type"].(string); ok && strings.TrimSpace(v) != "" {
			typ = v
		}
		types[typ]++
		if v, ok := m["text"].(string); ok && strings.TrimSpace(v) != "" {
			lastText = v
		}
		if v, ok := m["message"].(string); ok && strings.TrimSpace(v) != "" {
			lastText = v
		}
	}
	if total == 0 {
		return nil
	}
	return map[string]any{
		"total_events": total,
		"types":        types,
		"last_text":    clipped(lastText, 400),
	}
}
