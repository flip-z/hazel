package hazel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func InitRepo(ctx context.Context, root string) error {
	_ = ctx

	if err := ensureDir(hazelDir(root)); err != nil {
		return err
	}
	for _, d := range []string{tasksDir(root), runsDir(root), archiveDir(root), filepath.Join(hazelDir(root), "export"), filepath.Join(hazelDir(root), "templates")} {
		if err := ensureDir(d); err != nil {
			return err
		}
	}

	if !exists(boardPath(root)) {
		b := &Board{Version: 1, Tasks: []*BoardTask{}}
		if err := writeYAMLFile(boardPath(root), b); err != nil {
			return err
		}
	}

	if !exists(configPath(root)) {
		cfg := defaultConfig()
		if err := writeYAMLFile(configPath(root), &cfg); err != nil {
			return err
		}
	}

	// Templates (used by humans; Hazel won't auto-create task instances without explicit command).
	templates := map[string]string{
		filepath.Join(hazelDir(root), "templates", "task.md"): templateTaskMD,
		filepath.Join(hazelDir(root), "templates", "impl.md"): templateImplMD,
	}
	for p, body := range templates {
		if !exists(p) {
			if err := writeFileAtomic(p, []byte(body), 0o644); err != nil {
				return err
			}
		}
	}

	usagePath := filepath.Join(hazelDir(root), "USAGE.md")
	if !exists(usagePath) {
		if err := writeFileAtomic(usagePath, []byte(templateUsageMD), 0o644); err != nil {
			return err
		}
	}

	agentsPath := filepath.Join(root, "AGENTS.md")
	if !exists(agentsPath) {
		if err := writeFileAtomic(agentsPath, []byte(defaultAgentsMD), 0o644); err != nil {
			return err
		}
	} else {
		// Append contract section if missing.
		b, err := os.ReadFile(agentsPath)
		if err != nil {
			return err
		}
		if !containsAgentsContract(string(b)) {
			updated := string(b)
			if len(updated) > 0 && updated[len(updated)-1] != '\n' {
				updated += "\n"
			}
			updated += "\n" + defaultAgentsContractSection
			if err := writeFileAtomic(agentsPath, []byte(updated), 0o644); err != nil {
				return err
			}
		}
	}

	fmt.Println("Initialized .hazel/")
	return nil
}

func containsAgentsContract(s string) bool {
	return strings.Contains(s, "## Agent Contract")
}

const templateTaskMD = `# Task

## Summary

## Context / Why

## Acceptance Criteria

## Non-Goals

## Links


<!-- HAZEL-CONFIG
hazel:
  # Pastel card background key (one of: peach, mint, sky, lilac, lemon, blush, sage, sand, ice, cloud)
  color: ""
  # Priority (HIGH, MEDIUM, LOW). If empty, Hazel treats it as "unset".
  priority: ""
HAZEL-CONFIG -->
`

const templateImplMD = `# Implementation (Agent-Owned)

## Plan

## Constraints / Tradeoffs

## File Touch List

## Checklist (Mapped To Acceptance Criteria)
`

const templateUsageMD = `# Using Hazel

Hazel is a filesystem-first work queue. The repository is the source of truth.

## Quick Start

1. Start the UI:

   Background (default):
     hazel up

   Foreground:
     hazel up --foreground

2. Create tasks from the UI (BACKLOG).
3. Move a task to READY when the intent is complete.

## Automation

Hazel's automation tick is:
  hazel run

To run it periodically, choose one:

1. Run the scheduler loop inside the UI process:
     hazel up --scheduler

2. Cron (example: every minute):
     */1 * * * * cd /path/to/repo && hazel run >> .hazel/cron.log 2>&1

## Hooking Up An Agent (Codex/Claude/etc.)

Edit .hazel/config.yaml and set:

- agent_command: the invocation Hazel will run after it dispatches exactly one READY task to ACTIVE
- enable_runs: capture stdout/stderr to .hazel/runs/

Important: agent_command is not a fixed task id. Hazel sets env vars per-dispatch so the same command can handle any task.

Hazel sets these env vars when running agent_command:

- HAZEL_ROOT: repo root
- HAZEL_TASK_ID: task id (e.g. HZ-0001)
- HAZEL_TASK_DIR: task directory
- HAZEL_AGENT_PACKET: path to agent_packet.md

Typical agent_command patterns:

- run your agent against $HAZEL_AGENT_PACKET and/or $HAZEL_TASK_DIR/task.md
- have the agent write engineering notes to $HAZEL_TASK_DIR/impl.md and code changes in the repo

## What Controls Agent Behavior?

There are two separate concepts:

1. agent_command (in .hazel/config.yaml)
   - The how: which program to run (Codex/Claude/etc.) and the prompt/flags to pass it.
   - Hazel calls this command once per dispatched task.

2. AGENTS.md
   - The contract: what the agent is allowed to edit and what it must not rewrite.
   - You should make your agent_command prompt tell the agent to follow AGENTS.md.

Hazel itself does not enforce the contract yet; it is a convention you enforce via the agent prompt and code review.

## Does The Agent "Handle The Board"?

By design, Hazel handles the board mechanics:

- Hazel selects the next READY task, moves it to ACTIVE, and then runs agent_command.
- The agent should focus on the dispatched task directory and repo code.

If you want an agent to manage board.yaml (triage, reorder, create tasks, move statuses), that is a different mode and not what hazel run does today.
You could build an operator agent separately, but it would need explicit permission to edit .hazel/board.yaml and would change the contract semantics.

## Example: OpenAI Codex

If you have the Codex CLI installed (npm i -g @openai/codex), you can set agent_command to run Codex non-interactively via codex exec:

  agent_command: codex exec "Follow AGENTS.md. Work on task $HAZEL_TASK_ID. Read $HAZEL_TASK_DIR/task.md and write engineering work to $HAZEL_TASK_DIR/impl.md. Make code changes in the repo as needed."

If you want Codex to be allowed to edit files automatically, you can add --full-auto (be careful with permissions/sandbox settings):

  agent_command: codex exec --full-auto "Follow AGENTS.md. Work on task $HAZEL_TASK_ID. Read $HAZEL_TASK_DIR/task.md and write engineering work to $HAZEL_TASK_DIR/impl.md. Make code changes in the repo as needed."

Adjust flags (sandbox/approval) to your environment and risk tolerance.
`

const defaultAgentsMD = `# AGENTS

This repository uses Hazel's agent contract.

` + defaultAgentsContractSection

const defaultAgentsContractSection = `## Agent Contract

Rules:

* Humans own: task.md (intent, acceptance, context, links)
* Agents own: impl.md, artifacts/, code
* Agents must not edit task.md (avoid mixing intent/requirements with implementation details)
* Agent must not remove or rewrite human acceptance criteria

Failure to follow contract is a bug.
`
