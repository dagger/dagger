---
name: tui-console
description: Drive any dagger command's live pretty TUI headlessly over HTTP via DAGGER_TUI_CONSOLE — GET the screen, POST keystrokes, navigate spans/checks/tests, zoom to a span — to explore or reproduce TUI rendering interactively without a pty. Use when investigating how a run or trace renders, reproducing a rendering bug, or operating the idtui frontend by hand.
---

# TUI Console

`DAGGER_TUI_CONSOLE=<addr>` makes any dagger command serve its **live pretty
TUI over HTTP** on a headless terminal instead of attaching to a real one. You
drive the real `frontendPretty` (and the command's real work) with `curl`
instead of keystrokes, so what you see is exactly what a user sees at a tty.

Use it to operate the interactive TUI by hand — explore how a run/trace renders,
reproduce a rendering bug, expand a check/test to see its detail, or step through
navigation — when you don't have (or don't want) a pty.

Complementary to `tui-qa`: that skill records a real pty session (asciinema) for
timing/hang/"what the user saw at time T" analysis; this one *interactively
drives* the live UI. Reach for `tui-qa` for timing and redraw behavior, this for
navigating and inspecting state.

This is a dev/debug affordance: off by default, bound to the address you give
(use localhost).

## Enable it

Prefix **any** dagger command with `DAGGER_TUI_CONSOLE=:<port>`. It's orthogonal
to what the command does or where its data comes from — it only changes how the
TUI is presented. From a dagger checkout, run the dev binary in the background
and wait for it to come up:

```bash
# any command works: call, check, a trace, ...
env DAGGER_TUI_CONSOLE=:7777 go run ./cmd/dagger call test &

# poll until ready:
for i in $(seq 1 60); do
  curl -s -o /dev/null -w '%{http_code}' localhost:7777/help | grep -q 200 && break
  sleep 2
done
```

(An installed `dagger` binary works the same: `DAGGER_TUI_CONSOLE=:7777 dagger …`.)

## Drive it

All endpoints return `text/plain`; screens are ANSI-stripped (add `?raw=1` to
keep color).

```bash
curl -s localhost:7777/screen                  # current frame
curl -s --data 'right'     localhost:7777/key  # expand the focused span
curl -s --data 'down down' localhost:7777/key  # navigate (keys: tuist.ParseKey names)
curl -s --data 'TestFoo'   localhost:7777/type # type a literal string (e.g. into / search)
curl -s 'localhost:7777/spans?q=TestFoo'       # list loaded spans matching a name
curl -s --data '<spanHex>' localhost:7777/zoom # jump straight to a span
curl -s --data '120x12'    localhost:7777/resize # resize the terminal (cols x rows)
curl -s localhost:7777/help                    # endpoints + keymap
```

State accumulates across requests like a real session. Each request settles
briefly so background lazy fetches land; if a screen still looks mid-load, just
GET `/screen` again.

The screen is **viewport-clipped to the current terminal size**, exactly like a
real tty: when the rendered frame is taller than `rows`, only the bottom `rows`
lines are shown and the top scrolls offscreen (tuist's alt-screen behaviour). So
`/resize` to a small height is how you reproduce overflow-only rendering bugs —
e.g. a focused row whose own promoted tests/logs are taller than the viewport,
pushing its own header off the top. Either dimension may be 0/omitted to keep
the current value (`x12` changes only rows).

### Keymap (for `/key`)

`←↑↓→` / `h j k l` move · `right`/`l` expand · `left`/`h` collapse · `enter`
zoom · `esc` back out · `r` jump to error origin · `L` logs · `+`/`-` verbosity ·
`/` search · `T` tests view. A `down*3` token repeats a key; commas or spaces
separate keys (`"down,down,right"`).

To type into the search field, open it with `/` then POST the query to `/type`
(which is *not* tokenized — spaces are typed verbatim), then submit with `enter`:

```bash
curl -s --data '/'           localhost:7777/key
curl -s --data 'TestFoo'     localhost:7777/type
curl -s --data 'enter'       localhost:7777/key   # jump to the match
```

### Typical flow: find a failure and inspect it

The `/spans` status column is `ERROR` when the span itself errored and `FAIL`
when it only *caused* a failure via a link (a test/check whose error rides on a
descendant or linked span), so match both:

```bash
SID=$(curl -s 'localhost:7777/spans?q=TestThatFailed' | grep -E 'ERROR|FAIL' | head -1 | awk '{print $1}')
curl -s --data "$SID" localhost:7777/zoom
curl -s --data 'right' localhost:7777/key      # expand for logs/detail
```

## Cleanup

Stop the background command (Ctrl-C / kill) when done — the console shuts down
gracefully on signal.

## Notes

- **Command requirements are unchanged.** The console doesn't add any. `dagger
  trace <id>` still needs Cloud access (run `dagger login`, or point
  elsewhere with `DAGGER_CLOUD_URL`); `call`/`check` still run locally. The
  console is just the presentation layer.
- Screens default to 120x40; verbosity is driven by the `+`/`-` keys.
- Implementation: `dagql/idtui/frontend_console.go` (`runWithConsole` + the HTTP
  handlers); forced on regardless of tty in `internal/cmd/dagger/main.go`.
- The deterministic counterpart for CI is the in-memory `traceSession` smoke test
  (`dagql/idtui/trace_session_test.go`), which asserts lazy-fetch behavior without
  a network or HTTP.
