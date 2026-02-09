
# Hazel — Project Work Queue

**Hazel** is a filesystem-first project work queue with a lightweight web UI and optional automation.
It is human-usable project management tooling that also supports deterministic coding-agent workflows.

Hazel treats the **project repository as the unit of truth**.

---

## Goals

* Provide a **simple, durable task system** embedded in a project repo
* Be fully usable by **humans only**, **agents only**, or **both**
* Keep all task intent **diff-friendly and reviewable**
* Enable deterministic, safe **agent task intake**
* Require **no external services**
* Run as a **single local binary**

---

## Non-Goals
* SaaS, auth, or remote collaboration
* Prescriptive SDLC enforcement

---

## Canonical Data del

**Filesystem is canonical.**
All state must be reconstructible from files.

### Directory Layout

```
/AGENTS.md
/.hazel/
  board.yaml
  config.yaml
  tasks/
    HZ-0001/
      task.md
      impl.md
      artifacts/
  runs/
```

### `board.yaml`

Single source of truth for task state and ordering.

Per task:

* `id`
* `title`
* `status` ∈ `BACKLOG | READY | ACTIVE | REVIEW | DONE`
* `priority` (optional; stored in `task.md` hazel config as `HIGH|MEDIUM|LOW`)
* `order` (optional int; stable ordering within a column)
* `created_at`
* `updated_at`
* `deps` (optional list of task IDs)

### Task Files (Markdown)

**`task.md`** (human-owned)

* Summary
* Context / why
* Acceptance criteria
* Non-goals
* Links (PRs, commits)
* Task-local Hazel config (YAML frontmatter), e.g. card color

**`impl.md`** (agent-owned)

* Implementation plan
* Constraints / tradeoffs
* File touch list
* Final checklist mapped to acceptance criteria

---

## Status Semantics

* **BACKLOG** — incomplete intent
* **READY** — safe to start, acceptance defined
* **ACTIVE** — currently being worked
* **REVIEW** — requires human review
* **DONE** — reviewed and accepted

Hazel automation:

* **Only pulls from `READY`**
* **Moves tasks to REVIEW after the agent command finishes**

---

## Ordering & Determinism

Task selection order:

1. `priority` (nulls last)
2. `order` (nulls last)
3. `created_at`

This applies to both UI rendering and agent pull behavior.

---

## CLI Interface

### Core Commands

```
hazel init
hazel up
hazel run
hazel export --html
hazel archive [--before DATE]
hazel doctor
```

#### `hazel init`

* Creates `hazel/` structure
* Generates `board.yaml`, `config.yaml`
* Creates task templates
* Writes `AGENTS.md` section

#### `hazel up`

* Runs local web UI
* Optionally runs scheduler loop

#### `hazel run`

Single automation tick:

1. Enrichmedispatch
3. Reconciliation

#### `hazel export --html`

* Generates static HTML snapshot (read-only)

#### `hazel archive`

* Moves DONE tasks to `hazel/archive/`
* Updates `board.yaml`

---

## Web UI

**Server-rendered HTML + HTMX**

Features:

* Kanban board
* Drag/drop ordering
* Status change
* Priority editing
* Task detail view (renders Markdown)
* Filter toggle (hide DONE)
* Run log view

UI constraints:

* Edits `board.yaml`
* May edit `task.md` only via explicit user save action
* Creates files only via explicit user action

---

## Automation / Cron Behavior

Enabled via `config.yaml`.

### Enrichment

Reserved for future agent-owned channels (no core files are modified today).

### Agent Dispatch

* Select next task from `READY`
* Move to `ACTIVE`
* Generate `agent_packet.md` (derived summary)
* Invoke configured agent command
* Capture logs in `.hazel/runs/`

### Reconciliation

Agent may:

* Append implementation details

---

## Agent Contract (`AGENTS.md`)

Rules:

* Humans own: `task.md` (intent, context, acceptance criteria, links)
* Agents own: `impl.md`, `artifacts/`, and code
* Agents must not edit `task.md` (avoid mixing intent/requirements with implementation details)
* Agent must not remove or rewrite human acceptance criteria

Failure to follow contract is a bug.

---

## Configuration (`config.yaml`)

* `port`
* `run_interval_seconds`
* `agent_command`
* `enable_enrichment`
* `enable_runs`
* `ui_hide_done_by_default`

---

## Cost / Run Tracking (Optional)

If enabled:

* Each agent run writes:

  * provider
  * model
  * token counts
  * cost
  * timestamps
* UI aggregates per task
* Hazel does not infer costs

---

## Design Constraints

* Markdown + YAML + filesystem only
* SQLite allowed only as cache/index
* Deletable DB must be rebuildable
* No required internet access
* No AI-specific assumptions in core schema

---

## Out of Scope

* Global task orchestration
* Cross-repo dependency resolution
* Auth / permissions
* Auto-spec generation
* Business workflow enforcement

---

**Hazel is a work surface, not a manager.**
