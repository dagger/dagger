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
credential setup needed). To resume a previously auto-saved conversation, pass
`start(session: <file>)` — the file must keep its `<uuid>.json` name (it is
mounted where the CLI's session loader looks) — then start with
args: ["agent", "-r=<uuid>"].

Dagger Cloud auth is NOT inherited from the outer session: the CLI under test
reads it from the module's `cloudCredentials` setting, configured in
dagger.toml (`cloudCredentials = "file://~/.config/dagger/credentials.json"`)
and mounted where the CLI's auth code looks
(`$XDG_CONFIG_HOME/dagger/credentials.json`). Without it, commands that talk to
Cloud behave as logged out.

Profiling the live TUI process (for a wedged or busy input/render loop): the
runner also serves the CLI's PPROF debug server on a separate listener, so
these respond even when the console endpoints hang.

- `goroutines` dumps every goroutine's stack — the first stop for "what is the
  UI loop blocked on"; look for the goroutine holding consoleMu.
- `cpuProfile(seconds)` samples CPU while you reproduce the load and returns a
  `go tool pprof -top` table.
- `heapProfile` snapshots the heap the same way.

If engine-lab tools are available, their start tool prints a tcp://<host>:1234
endpoint — pass it as `start(engine: ...)` to run the TUI against THAT engine
instead of a fresh one, so its debug endpoints and logs observe exactly what
the TUI is driving. Caveats: no LLM auth in that mode (avoid for
`args: ["agent"]`), and restarting/stopping the lab engine breaks the attached
TUI session — start a new one after.
