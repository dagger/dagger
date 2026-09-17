package core

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// The fake app-server exercises the real attached service, process pipes,
// workspace mount, checkpoint commit, and cold continuation without a provider.
func (LLMSuite) TestHarnessAttachedServiceWorkspace(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	harness := c.Container().From("python:3.13-alpine").
		WithNewFile("/usr/local/bin/codex", fakeCodexHarness, dagger.ContainerWithNewFileOpts{Permissions: 0755}).
		WithWorkdir("/workspace")
	ws := c.Directory().WithNewFile("seed.txt", "original").AsWorkspace()
	conversation := c.LLM(dagger.LLMOpts{Model: emptyReplayModel}).WithWorkspace(ws).
		WithHarness(harness, dagger.LLMHarnessKindCodex).WithPrompt("first")
	for _, expected := range []string{"thread/start", "thread/resume"} {
		id, err := conversation.Step().ID(ctx)
		require.NoError(t, err)
		conversation, err = dagger.Load[*dagger.LLM](ctx, c, id)
		require.NoError(t, err)
		reply, err := conversation.LastReply(ctx)
		require.NoError(t, err)
		require.Equal(t, "fake reply", reply)
		output, err := conversation.Workspace().File("out.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "original:"+expected, output)
		conversation = conversation.WithPrompt("continue")
	}
}

const fakeCodexHarness = `#!/usr/bin/env python3
import json, os, pathlib, sys

def emit(value):
    print(json.dumps(value), flush=True)

thread_method = ""
for line in sys.stdin:
    request = json.loads(line)
    method = request.get("method", "")
    result = {}
    if method == "initialize":
        result = {"userAgent": "dagger-harness-test"}
    elif method in ("thread/start", "thread/resume"):
        thread_method = method
        result = {"thread": {"id": "test-thread"}}
    elif method == "turn/start":
        result = {"turn": {"id": "test-turn"}}
    elif method == "account/login/start":
        result = {"type": "apiKey"}
    if "id" in request:
        emit({"method": method, "id": request["id"], "response": result})
    if method != "turn/start":
        continue
    # Verify this really is a runtime with its local MCP endpoint configured.
    assert os.environ["DAGGER_SESSION_PORT"]
    assert os.environ["DAGGER_SESSION_HTTP_TOKEN"]
    seed = pathlib.Path("seed.txt").read_text()
    pathlib.Path("out.txt").write_text(seed + ":" + thread_method)
    params = {"threadId": "test-thread", "turnId": "test-turn"}
    emit({"method": "turn/started", "params": {"threadId": "test-thread", "turn": {"id": "test-turn", "status": "inProgress"}}})
    user = {"type": "userMessage", "id": "test-user", "clientId": request["params"]["clientUserMessageId"]}
    for lifecycle in ("item/started", "item/completed"):
        emit({"method": lifecycle, "params": dict(params, item=user)})
    emit({"method": "item/agentMessage/delta", "params": dict(params, itemId="test-assistant", delta="fake reply")})
    emit({"method": "turn/completed", "params": {"threadId": "test-thread", "turn": {"id": "test-turn", "status": "completed"}}})
`
