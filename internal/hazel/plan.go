package hazel

import (
	"fmt"
	"strings"
)

const planStart = "<!-- HAZEL-PLAN START -->"
const planEnd = "<!-- HAZEL-PLAN END -->"

func PlanTask(root, id string) error {
	if err := ensureTaskScaffold(root, id); err != nil {
		return err
	}
	md, err := readTaskMD(root, id)
	if err != nil {
		return err
	}
	updated, err := upsertPlanBlock(md)
	if err != nil {
		return err
	}
	return writeTaskMD(root, id, updated)
}

func upsertPlanBlock(md string) (string, error) {
	cfg, hasCfg, stripped, cfgBlock, err := splitHazelConfig(md)
	if err != nil {
		return "", err
	}
	_ = cfg
	// Remove any existing plan block from stripped content.
	base := removePlanBlock(stripped)

	bodyForPlan := base
	if b, err := stripTaskConfigForRender(base); err == nil {
		bodyForPlan = b
	}

	plan := generatePlan(bodyForPlan)
	out := strings.TrimRight(base, " \t\r\n") + "\n\n" + planStart + "\n" + plan + "\n" + planEnd + "\n"
	if hasCfg {
		out = strings.TrimRight(out, " \t\r\n") + "\n" + cfgBlock
	} else {
		// If task.md has no config, keep it that way. Color/priority setters add config explicitly.
	}
	return out, nil
}

func removePlanBlock(md string) string {
	i := strings.Index(md, planStart)
	if i < 0 {
		return md
	}
	j := strings.Index(md[i:], planEnd)
	if j < 0 {
		return md
	}
	j = i + j + len(planEnd)
	return strings.TrimRight(md[:i]+md[j:], " \t\r\n") + "\n"
}

func generatePlan(taskBody string) string {
	ac := extractSection(taskBody, "Acceptance Criteria")
	var checklist []string
	for _, line := range strings.Split(ac, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimPrefix(line, "*")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		checklist = append(checklist, "- [ ] "+line)
	}
	if len(checklist) == 0 {
		checklist = []string{"- [ ] (fill in acceptance checklist)"}
	}

	// Deterministic plan template. No AI; meant for workshop + refinement.
	var sb strings.Builder
	sb.WriteString("## Plan (Draft)\n\n")
	sb.WriteString("### Approach\n\n")
	sb.WriteString("- (fill in)\n\n")
	sb.WriteString("### Work Breakdown\n\n")
	sb.WriteString("- (fill in)\n\n")
	sb.WriteString("### Risks / Open Questions\n\n")
	sb.WriteString("- (fill in)\n\n")
	sb.WriteString("### Acceptance Checklist\n\n")
	sb.WriteString(strings.Join(checklist, "\n"))
	sb.WriteString("\n")
	return sb.String()
}

func extractSection(md string, heading string) string {
	needle := "## " + heading
	i := strings.Index(md, needle)
	if i < 0 {
		return ""
	}
	rest := md[i+len(needle):]
	// Find next "## " heading.
	next := strings.Index(rest, "\n## ")
	if next < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:next])
}

func splitHazelConfig(md string) (cfg taskHazelMeta, has bool, without string, cfgBlock string, err error) {
	fm, ok, without, err := parseHazelConfigBlock(md)
	if err != nil {
		return taskHazelMeta{}, false, md, "", err
	}
	if !ok {
		return taskHazelMeta{}, false, md, "", nil
	}
	block, err := formatHazelConfigBlock(fm)
	if err != nil {
		return taskHazelMeta{}, false, md, "", fmt.Errorf("format hazel config: %w", err)
	}
	return fm.Hazel, true, without, strings.TrimLeft(block, "\n"), nil
}

