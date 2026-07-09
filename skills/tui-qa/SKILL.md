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
  --command 'dagger sdk install go && dagger module init go demo' \
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

## Driving an interactive dagger TUI from an agent (tmux)

The `run`/`record` helpers above capture a command; to *drive* an interactive
TUI (type into a prompt, press keys, watch it react) you need a real pty plus
keystroke injection. Use `tmux`: `send-keys` injects, `capture-pane -p` reads the
rendered screen. This is how to QA `dagger shell --model …`, `dagger llm`, or any
interactive prompt.

Two gotchas specific to dagger, both learned the hard way:

- **`DAGGER_PROGRESS=tty` is mandatory.** The CLI calls `idtui.RunningInAgent()`
  (env vars like `CLAUDECODE`), and when true it forces the non-interactive
  *report* frontend — so the live TUI never engages no matter the tty. Worse,
  `dagger shell --model` in report mode currently **panics** (nil `keymapBar` in
  `startShell`). `DAGGER_PROGRESS=tty` is checked before that agent-detection
  (`internal/cmd/dagger/main.go`), so it restores the interactive Pretty TUI.
- **You need a real pty.** `progress=tty` requires `hasTTY`; a tmux pane is a real
  pty (`[ -t 1 ]` is true inside it), so this satisfies it. Bare piped exec does
  not. (`DAGGER_TUI_CONSOLE` forces Pretty over HTTP but does **not** currently
  drive interactive shell/prompt mode — it shows the progress tree, not the
  prompt overlay — so prefer tmux for interactive QA.)

Recipe:

```bash
# 1. Launch in a detached pane, in the target workspace, with progress forced.
tmux new-session -d -s qa -x 200 -y 50
tmux send-keys -t qa 'cd /path/to/workspace && DAGGER_PROGRESS=tty \
  /repo/hack/with-dev /repo/bin/dagger shell --model claude-opus-4-8' Enter

# 2. Poll capture-pane until the bowtie prompt (⋈, "esc nav mode · > run prompt")
#    appears — that means the interactive TUI (not report mode) engaged.
tmux capture-pane -t qa -p | tail

# 3. Drive it. `-l` sends a literal string; key names (Enter, C-c) without -l.
tmux send-keys -t qa -l '>'                         # enter LLM prompt mode
tmux send-keys -t qa -l 'Edit notes.txt to add a line.'
tmux send-keys -t qa Enter                          # submit

# 4. Observe by polling capture-pane; tool calls (SchemaSearch, DangEval, …) and
#    the assistant's text stream into the pane. Inspect written files on disk too.
tmux capture-pane -t qa -p | sed '/^$/d' | tail -30

# 5. Clean up.
tmux kill-session -t qa
```

Notes: `send-keys -l` is literal (preserves spaces/quotes); without `-l`, args are
tmux key names (`Enter`, `Escape`, `C-c`). Poll with separate calls rather than a
long foreground `sleep`. For timing/hang analysis of the same session, record the
pane with `asciinema` and feed the `.cast` to `tui_qa.py analyze`.

## Reading the screen reliably

General to any interactive TUI captured over tmux, not just dagger:

- **Un-wrap before parsing.** `capture-pane -p` hard-wraps at the pane width,
  splitting long lines mid-token so `grep`/`awk` see garbage (a panic stack
  becomes unreadable). Add `-J` to join wrapped lines when you parse
  programmatically.
- **Include scrollback.** The screen you care about — a final report, an error,
  a summary — often scrolls above the visible viewport. `capture-pane -p -S -<N>`
  prepends N lines of history; without it you only get the last `-y` rows.
- **Check for crashes explicitly.** A TUI on the alt-screen can die into a stack
  trace a naive "final.txt" glance misses. Grep the pane (or redirect the process
  with `2>err.log`) for `panic:`, `fatal error`, `concurrent map`,
  `goroutine \d+ \[`.
- **Exit gracefully, not abruptly.** Use the app's own quit path so its teardown,
  final render, and flushes run. `tmux kill-session` / SIGKILL skips all of that,
  so you lose the end-of-run output and any deferred cleanup. Learn the real quit
  (often EOF/`C-d` on an *empty* input line, a quit key, or an `exit` command) and
  confirm the process actually exited before reading the "final" screen.
