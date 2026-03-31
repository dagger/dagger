# LLM Session Save/Reload Planning

## Problems

### 1. Duplicate saves: new UUID per prompt
Currently `AutoSaveSession` is called after every prompt response, and each
call generates a fresh UUIDv7. This means a single conversation produces N
session files instead of 1.

**Fix:** Assign the session UUID once (on first prompt) and store it on the
`shellCallHandler`. Subsequent saves update the same file in-place rather
than creating a new one.

- [x] Add `sessionUUID` field to `shellCallHandler`
- [x] Generate UUID on first prompt only
- [x] `AutoSaveSession` writes to the existing file instead of creating new ones

### 2. Branching
The TUI supports pressing `b` on a history item to branch the LLM
conversation from that point (via `BranchFromID`). A branch should be
treated as a *new* session (new UUID, new file) so the original session
remains intact and the branch gets its own save file.

- [x] `BranchFromID` assigns a new `sessionUUID` (and clears `initialPrompt`
  so it picks up the next user prompt as the new name)
- [x] The branched session auto-saves normally from that point

### 3. Replaying history on resume
When resuming a session, `LoadLLMFromID` reconstitutes the LLM state in the
engine, but no telemetry spans are re-emitted, so the TUI scrollback is
empty. The user sees a blank screen despite having a full conversation
loaded.

**Approach:** After loading the session, use `LLM.History()` (which returns
rendered markdown strings per message) to emit synthetic telemetry spans
that the TUI can display. Each history entry becomes a span with the
appropriate `LLMRole` attribute so the TUI renders it like a normal
user/assistant exchange.

- [x] Refactored: extracted `emitMessageSpan`, `emitUserMessageSpan`,
  `emitAssistantMessageSpan` from `step()` in `core/llm.go`
- [x] Added `LLM.Replay(ctx)` method that walks all messages and emits spans
- [x] Exposed `replay` as a GraphQL `NodeFunc` in `core/schema/llm.go`
- [x] Added `Replay()` to Go SDK (`sdk/go/dagger.gen.go`)
- [x] `LoadSession` calls `Replay` after loading so the TUI shows history

## Implementation order
1. ~~Fix duplicate saves (simplest, highest impact)~~ ✅
2. ~~Handle branching (small delta on top of #1)~~ ✅
3. ~~Replay history on resume (needs telemetry span emission)~~ ✅

## Architecture notes

### Centralized message span emission (`core/llm.go`)
The three provider implementations (OpenAI, Anthropic, Google) each created
their own assistant-response telemetry spans inside `SendQuery`. The `step()`
function created user/system spans inline. This made replay difficult since
there was no single code path for "emit spans for a message."

The refactor extracts three functions:
- `emitMessageSpan(ctx, msg, callDigest)` — dispatcher by role
- `emitUserMessageSpan(ctx, msg, callDigest)` — user/system messages
- `emitAssistantMessageSpan(ctx, msg, callDigest)` — assistant messages,
  splitting thinking vs text/tool-call blocks into separate spans (matching
  the Anthropic streaming behavior)

`step()` now calls `emitMessageSpan` for user messages. Provider-created
`DisplaySpans` still handle the live assistant response (since those need
streaming support). `Replay` uses `emitMessageSpan` for all roles.

Note: provider `DisplaySpans` are still created by each provider during
live `SendQuery` calls. A future refactor could unify those too, but it
would require changing the streaming architecture.
