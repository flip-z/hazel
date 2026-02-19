package hazel

import (
	"fmt"
	"os"
	"strings"
)

const planProposalFile = "plan.md"

func planProposalPath(root, taskID string) string {
	return taskFile(root, taskID, planProposalFile)
}

func readPlanProposal(root, taskID string) (string, error) {
	b, err := os.ReadFile(planProposalPath(root, taskID))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func clearPlanProposal(root, taskID string) error {
	err := os.Remove(planProposalPath(root, taskID))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func mergeTaskMDWithPlanProposal(taskMD, planMD string) (string, error) {
	if strings.TrimSpace(planMD) == "" {
		return "", fmt.Errorf("plan proposal is empty")
	}

	// Keep proposal content clean even if the planner accidentally included config.
	_, hasPlanCfg, planWithoutCfg, err := parseHazelConfigBlock(planMD)
	if err != nil {
		return "", err
	}
	base := strings.TrimRight(planMD, " \t\r\n")
	if hasPlanCfg {
		base = strings.TrimRight(planWithoutCfg, " \t\r\n")
	}

	// Preserve existing task metadata (color/priority/git) regardless of proposal content.
	taskCfg, hasTaskCfg, _, err := parseHazelConfigBlock(taskMD)
	if err != nil {
		return "", err
	}
	if !hasTaskCfg {
		return base + "\n", nil
	}

	block, err := formatHazelConfigBlock(taskCfg)
	if err != nil {
		return "", err
	}
	return base + "\n" + block, nil
}
