---
name: tui-qa
description: QA CLI and TUI applications with asciinema recordings, timestamped terminal snapshots, and hang/timing analysis. Use when validating terminal output, progress behavior, delays, or what a user would see at a given moment.
---

# TUI QA

Use this skill for operator-style QA of terminal applications.

Prefer the bundled helper at `scripts/tui_qa.py` over ad hoc shell pipelines. It standardizes artifacts, snapshot capture, and timing analysis so follow-up sessions can inspect the same evidence.

## Backend

This skill requires `asciinema`.

Artifact meanings:

- `.cast`: source of truth for timing and terminal behavior
- `final.txt`: final rendered screen as plain text
- `snapshots/*.txt`: rendered screen at specific timestamps
- `.raw`: optional low-level byte stream for terminal-control debugging
- `report.json`: machine-readable timings, findings, and artifact paths
- `report.md`: short human-readable QA summary

Use `.cast` for time-sensitive behavior. Use text snapshots for "what the user saw at time T". If terminal control behavior itself is suspect, inspect the `.cast` replay rather than relying on `.txt`.

## Workflow

1. State the operator expectation before recording.

   - expected first visible response
   - expected milestones
   - expected final screen or files written
   - acceptable delays

1. Record and analyze in one step when possible:

```bash
python3 skills/tui-qa/scripts/tui_qa.py run \
  --name workspace-install \
  --command 'dagger install github.com/dagger/dagger/modules/wolfi@main' \
  --workdir /path/to/repo \
  --snapshot-at 0.5 \
  --snapshot-at 2 \
  --milestone 'Initialized workspace' \
  --milestone 'Installed module'
```

1. Review:

   - `report.md` for the summary
   - `snapshots/*.txt` for point-in-time screens
   - `session.cast` with `asciinema play` when timing or redraw behavior matters

1. Classify issues:

   - content bug
   - timing bug
   - hard hang: no output for too long
   - semantic hang: output continues but no meaningful milestone progress
   - polish issue

## Commands

`record`

- Use for manual or interactive sessions.
- Stores `session.cast` and `meta.json`.
- If the current shell is not attached to a tty, the helper automatically records in headless mode.

```bash
python3 skills/tui-qa/scripts/tui_qa.py record \
  --name interactive-playground
```

```bash
python3 skills/tui-qa/scripts/tui_qa.py record \
  --name module-init \
  --command 'dagger module init --sdk=go demo' \
  --workdir /tmp/playground
```

`snapshot`

- Produces a plain-text screen at a chosen timestamp.
- The helper truncates the cast at the last full event at or before `--at`, then converts that truncated cast to text.

```bash
python3 skills/tui-qa/scripts/tui_qa.py snapshot \
  .qa/tui/module-init-20260404-120000/session.cast \
  --at 1.25
```

`analyze`

- Parses the event stream.
- Generates `final.txt`, snapshots, `report.json`, and `report.md`.
- Detects startup delay and hard hangs from periods with no output.
- If milestones are supplied, also reports semantic-hang candidates.

Default thresholds:

- startup warning: 2s
- startup failure: 5s
- idle warning: 10s
- idle failure: 30s
- semantic-hang warning: 120s

`run`

- Runs `record`, then `analyze`.
- This is the default path for non-interactive QA.

## Guidance

- Treat `.cast` as the authority for timing.
- Treat `final.txt` as the authority for final rendered text.
- When low-level terminal control bytes matter, export `.raw` directly with `asciinema convert -f raw session.cast session.raw`.
- Use milestones for higher-level progress checks. Without milestones, semantic-hang analysis is intentionally reported as not evaluated.
- Input events do not count as progress.
- Output events that only repaint the terminal still count for hard-hang timing. Use milestone timing and snapshots to decide whether that output felt meaningfully progressive.
- For commands that write files, inspect the filesystem after the run in addition to the terminal artifacts.

## Examples

Batch CLI:

```bash
python3 skills/tui-qa/scripts/tui_qa.py run \
  --name help-output \
  --command 'dagger --help' \
  --snapshot-at 0.1
```

Progress UI:

```bash
python3 skills/tui-qa/scripts/tui_qa.py run \
  --name generate \
  --command 'dagger generate' \
  --milestone 'Generated' \
  --milestone 'done'
```

Manual recording, then analysis:

```bash
python3 skills/tui-qa/scripts/tui_qa.py record --name manual-flow
python3 skills/tui-qa/scripts/tui_qa.py analyze .qa/tui/manual-flow-*/session.cast
```

Specific screen sample:

```bash
python3 skills/tui-qa/scripts/tui_qa.py snapshot \
  .qa/tui/generate-20260404-120000/session.cast \
  --at 12.3 \
  --label before-finish
```
