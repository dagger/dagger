---
name: tui-qa
description: QA the live Dagger TUI with the TuiQa tools, driving a real from-source dagger CLI's pretty TUI over HTTP (DAGGER_TUI_CONSOLE). Read before using the tui-qa tools to read the rendered screen, send keys, inspect spans and their dagui flags, reproduce rendering bugs, or drive the interactive `dagger agent` prompt.
---

# TUI QA

You can QA the live Dagger TUI with the TuiQa tools, which drive a real
from-source `dagger` CLI's pretty TUI over HTTP (the DAGGER_TUI_CONSOLE
affordance — see the `tui-console` skill).

Workflow:

- the tui-qa start tool (e.g. args: ["call", "test"]) builds the CLI from the
  workspace source, runs the command against a from-source engine, and starts
  serving the TUI.
- `screen` reads the current rendered terminal. Poll it to watch progress;
  startup takes a little while (build + engine connect), and the tools retry
  the connection for you.
- `key`, `typeText`, `resize`, `zoom`, `spans` drive and inspect the TUI,
  exactly like the endpoints in the `tui-console` skill.
- `span(spanHex)` inspects one span in depth (status, timing, dagui flags like
  internal/passthrough/roll-up, and the parent chain with each ancestor's
  flags) — use it to answer "why is this span hidden / why didn't its logs roll
  up". Get ids from `spans`.
- Check for crashes explicitly: grep the screen for "panic:", "fatal error".
- the tui-qa stop tool when done.

Interactive prompt mode works too: starting with args: ["agent"] brings up the
live `dagger agent` prompt. Drive it by `typeText`-ing a line into the editline
and `key enter` to submit; `key esc` toggles nav/input mode. The runner uses
experimentalPrivilegedNesting so it inherits the outer session's LLM auth (no
credential setup needed).

If engine-lab tools are available, their start tool prints a tcp://<host>:1234
endpoint — pass it as `start(engine: ...)` to run the TUI against THAT engine
instead of a fresh one, so its debug endpoints and logs observe exactly what
the TUI is driving. Caveats: no LLM auth in that mode (avoid for
`args: ["agent"]`), and restarting/stopping the lab engine breaks the attached
TUI session — start a new one after.
