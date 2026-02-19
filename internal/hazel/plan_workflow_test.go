package hazel

import (
	"strings"
	"testing"
)

func TestMergeTaskMDWithPlanProposalPreservesTaskConfig(t *testing.T) {
	task := `# Task

## Summary
Old

<!-- HAZEL-CONFIG
hazel:
  color: mint
  priority: HIGH
HAZEL-CONFIG -->
`
	plan := `# Task

## Summary
New summary

## Acceptance Criteria
- item

<!-- HAZEL-CONFIG
hazel:
  color: peach
  priority: LOW
HAZEL-CONFIG -->
`

	got, err := mergeTaskMDWithPlanProposal(task, plan)
	if err != nil {
		t.Fatalf("mergeTaskMDWithPlanProposal error: %v", err)
	}
	if !strings.Contains(got, "New summary") {
		t.Fatalf("expected merged task to include proposal body")
	}
	if strings.Contains(got, "priority: LOW") {
		t.Fatalf("expected proposal config to be ignored")
	}
	if !strings.Contains(got, "priority: HIGH") || !strings.Contains(got, "color: mint") {
		t.Fatalf("expected original task config to be preserved")
	}
}

func TestMergeTaskMDWithPlanProposalRejectsEmptyPlan(t *testing.T) {
	_, err := mergeTaskMDWithPlanProposal(templateTaskMD, " \n\t")
	if err == nil {
		t.Fatalf("expected error for empty plan proposal")
	}
}
