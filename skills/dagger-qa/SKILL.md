---
name: dagger-qa
description: "Run QA scenarios against the Dagger workspace UX. Builds a dev engine from source via the playground, executes end-to-end test scenarios (workspace init, module creation, install, checks), and produces a concise tabular report for human review. Use when asked to: run QA, test workspace features, smoke test, check for regressions, verify the workspace UX works, run e2e tests manually, or validate a fix. Triggers on: QA, smoke test, workspace QA, run checks QA, test the UX, regression test, manual e2e."
---

# Dagger QA

Run standardized QA scenarios against the workspace UX using the playground (dev engine built from source). Produce concise, repeatable reports for human reviewers.

## Prerequisites

- Load the `engine-dev-testing` skill first -- it provides the playground and explains how to use it.
- The playground script is at `skills/engine-dev-testing/with-playground.sh` relative to the repo root.

## Running a QA Scenario

Each scenario is a shell script that runs inside the playground. Use the `run-qa.sh` helper:

```bash
PLAYGROUND_TIMEOUT=900 bash skills/engine-dev-testing/with-playground.sh "$(cat .claude/skills/dagger-qa/scenarios/workspace-basics.sh)"
```

Always use `run_in_background: true` on the Bash tool and `timeout: 600000` since builds take 3-5 min and scenarios add more time on top.

## Interpreting Output

The playground output is very verbose (engine build logs, service startup, debug traces). To extract the QA report, grep for the structured markers:

```bash
grep -E "^(── |EXIT:|Error:|PASS|FAIL|CRASH|QA REPORT|====)" <output_file>
```

### Result statuses

| Status | Meaning |
|--------|---------|
| PASS | Step exited 0 and produced expected output |
| FAIL | Step exited non-zero with an error message |
| CRASH | Engine panic (nil deref, goroutine stack trace) |
| TIMEOUT | Playground killed after PLAYGROUND_TIMEOUT seconds |

### Engine crashes

Look for `panic:` in the output. The stack trace shows the crash location. Trace backward from the top frame to find root cause (often a nil field on a partially-initialized struct). The panic location in the source code (e.g., `core/modtree.go:645`) tells you where to look.

## Writing New Scenarios

Each scenario is a self-contained shell script in `scenarios/`. Follow this pattern:

```bash
# 1. Define the step runner (captures exit code per step)
RESULTS=""
step() {
  STEP_NAME="$1"; shift
  echo "── $STEP_NAME ──"
  OUTPUT=$("$@" 2>&1)
  EC=$?
  echo "$OUTPUT"
  if [ $EC -eq 0 ]; then
    RESULTS="${RESULTS}| ${STEP_NAME} | PASS | exit=$EC |\n"
  else
    RESULTS="${RESULTS}| ${STEP_NAME} | FAIL | exit=$EC |\n"
  fi
  echo "EXIT: $EC"
  echo ""
  return $EC
}

# 2. Set up a clean project directory
mkdir -p /tmp/demo && cd /tmp/demo

# 3. Run steps (each is a single dagger command)
step "my-step" dagger some-command --flags

# 4. Print the report
echo "=============================="
echo "QA REPORT: <Scenario Name>"
echo "=============================="
echo "| Step | Status | Detail |"
echo "|------|--------|--------|"
printf "$RESULTS"
echo "=============================="
```

### Gotchas

- **`git` and `apk`** are available in the playground container (Wolfi base includes `apk-tools` and `git`). Use `apk add <pkg>` to install additional packages at runtime if needed.
- **`dagger module init` syntax**: Use `dagger module init --sdk=go <name>` (name as positional arg). Do NOT combine `--name=X` with a positional arg -- they conflict.
- **`dagger module init` auto-installs**: When run inside a workspace, `dagger module init --sdk=go ci` creates `.dagger/modules/ci/` AND adds it to `.dagger/config.toml` automatically. No separate `dagger install` needed.
- **Writing custom module source**: After `module init`, overwrite `.dagger/modules/<name>/main.go` with your custom source. The `mkdir -p` is needed since you're writing from the shell.
- **`dagger check` is hidden**: The command exists but is marked `Hidden: true`. It works fine, just doesn't show in `--help`.
- **Step isolation**: Each `dagger` invocation creates a new engine session. Steps are independent -- a crash in one doesn't prevent subsequent steps from running (unless the step function uses `set -e`).

## Report Format

Reports are designed for quick human scanning across many iterations:

```
==============================
QA REPORT: <Scenario Name>
==============================
| Step | Status | Detail |
|------|--------|--------|
| 1-workspace-init | PASS | exit=0 |
| 2-module-init | PASS | exit=0 |
| 3-list-checks | PASS | exit=0 |
| 4-run-checks | FAIL | exit=1 |
==============================
```

When presenting results to the user, render the table in markdown and add a brief summary of any failures or crashes, including the relevant error message or panic location.

## Available Scenarios

See `scenarios/` directory. Each file is a self-contained QA script.

- **workspace-basics.sh** -- Init workspace, create module with checks, list checks, run checks.
