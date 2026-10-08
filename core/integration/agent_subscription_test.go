package core

// Integration test for Subscription.agentEvents
// (hack/designs/graphql-subscriptions.md): an agent's lifecycle transitions
// pushed over graphql-sse, read by code. The subscriber is a script inside a
// container talking to its nested client (DAGGER_SESSION_PORT) — the path
// module code takes — so this also proves subscriptions need no proxying on
// the nested-client listener.

import (
	"context"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const agentEventsSubscriberScript = `import base64, json, os, sys, urllib.error, urllib.request

URL = "http://127.0.0.1:" + os.environ["DAGGER_SESSION_PORT"] + "/query"
AUTH = "Basic " + base64.b64encode((os.environ["DAGGER_SESSION_TOKEN"] + ":").encode()).decode()
AGENT = os.environ["AGENT_ID"]

def post(query, variables, stream=False):
    headers = {"Content-Type": "application/json", "Authorization": AUTH}
    if stream:
        headers["Accept"] = "text/event-stream"
    req = urllib.request.Request(URL, data=json.dumps({"query": query, "variables": variables}).encode(), headers=headers)
    try:
        return urllib.request.urlopen(req, timeout=120)
    except urllib.error.HTTPError as e:
        print("HTTP %d for %s: %s" % (e.code, query, e.read().decode()), file=sys.stderr)
        raise

def query(q, variables):
    with post(q, variables) as r:
        res = json.load(r)
    assert not res.get("errors"), res
    return res["data"]

# Minimal SSE parser (comments ignored, data lines joined).
def sse(resp):
    event, data = None, []
    for raw in resp:
        line = raw.decode().rstrip("\r\n")
        if line == "":
            if event is not None or data:
                yield event, "\n".join(data)
            event, data = None, []
        elif line.startswith(":"):
            continue
        else:
            field, _, value = line.partition(":")
            if value.startswith(" "):
                value = value[1:]
            if field == "event":
                event = value
            elif field == "data":
                data.append(value)

def agent_events(stream):
    for event, data in stream:
        assert event == "next", (event, data)
        payload = json.loads(data)
        assert not payload.get("errors"), payload
        yield payload["data"]["agentEvents"]

SUB = "subscription($agent: ID!, $after: Int) { agentEvents(agent: $agent, after: $after) { id seq state turnCompleted reply } }"
out = {}

# A subscription without the Accept header is refused, not truncated.
with post(SUB, {"agent": AGENT}) as r:
    out["refusal"] = json.load(r)

# Replay the whole log from a cursor, up to the completed first turn.
replay = []
with post(SUB, {"agent": AGENT, "after": 0}, stream=True) as r:
    out["contentType"] = r.headers.get("Content-Type")
    for ev in agent_events(sse(r)):
        replay.append(ev)
        if ev["state"] == "IDLE" and ev["turnCompleted"]:
            break
out["replay"] = replay

# Live: without a cursor the stream opens with the current state (the level
# check), then pushes the transitions a send causes while it is open.
live = []
with post(SUB, {"agent": AGENT}, stream=True) as r:
    events = agent_events(sse(r))
    out["level"] = next(events)
    query('query($id: ID!) { node(id: $id) { ... on Agent { send(message: "again") } } }', {"id": AGENT})
    for ev in events:
        live.append(ev)
        if ev["state"] == "IDLE" and ev["turnCompleted"]:
            break
out["live"] = live

# Every pushed event is a Node with an honest ID: it loads back.
out["reloaded"] = query('query($id: ID!) { node(id: $id) { ... on AgentEvent { seq state reply } } }', {"id": replay[-1]["id"]})["node"]

# The pull twin reads the same log.
out["pulled"] = query('query($id: ID!) { node(id: $id) { ... on Agent { events(after: 0) { seq state } } } }', {"id": AGENT})["node"]["events"]

print(json.dumps(out))
`

func (AgentRuntimeSuite) TestAgentEventsSubscription(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	model := cannedRecordingModel(ctx, t, c, c.LLM().
		WithPrompt("do the thing").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "done"},
		}).
		WithPrompt("again").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "done again"},
		}))
	worker := spawnAgent(ctx, t, c, spawnOpts{model: model, name: "w"})

	_, reply, err := worker.sendAndWait(ctx, t, "do the thing")
	require.NoError(t, err)
	require.Equal(t, "done", reply)

	out, err := c.Container().
		From(pythonImage).
		WithEnvVariable("AGENT_ID", worker.agentID).
		WithNewFile("/sub.py", agentEventsSubscriberScript).
		WithExec([]string{"python3", "/sub.py"}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Stdout(ctx)
	require.NoError(t, err)
	res := gjson.Parse(out)

	require.Contains(t, res.Get("refusal.errors.0.message").String(), "Accept: text/event-stream", out)
	require.Equal(t, "text/event-stream", res.Get("contentType").String())

	// The replayed log: seq 1 is the created state, seqs are contiguous, the
	// turn ran, and the IDLE edge that completed it carries the reply.
	replay := res.Get("replay").Array()
	require.NotEmpty(t, replay, out)
	for i, ev := range replay {
		require.EqualValues(t, i+1, ev.Get("seq").Int(), out)
	}
	require.Equal(t, "IDLE", replay[0].Get("state").String())
	require.False(t, replay[0].Get("turnCompleted").Bool())
	require.Contains(t, agentEventStates(replay), "RUNNING")
	last := replay[len(replay)-1]
	require.Equal(t, "done", last.Get("reply").String())

	// The level check reports the current state: the same IDLE the replay
	// ended on.
	require.Equal(t, last.Get("seq").Int(), res.Get("level.seq").Int(), out)
	require.Equal(t, "IDLE", res.Get("level.state").String())

	// Pushed live: the second turn's transitions, contiguous after the
	// level event, ending on the IDLE that carries the new reply.
	live := res.Get("live").Array()
	require.NotEmpty(t, live, out)
	for i, ev := range live {
		require.Equal(t, last.Get("seq").Int()+int64(i)+1, ev.Get("seq").Int(), out)
	}
	require.Contains(t, agentEventStates(live), "RUNNING")
	require.Equal(t, "done again", live[len(live)-1].Get("reply").String())

	// The pushed event's ID reloads to the same transition.
	require.Equal(t, last.Get("seq").Int(), res.Get("reloaded.seq").Int(), out)
	require.Equal(t, "IDLE", res.Get("reloaded.state").String())
	require.Equal(t, "done", res.Get("reloaded.reply").String())

	// The pull twin returns the same log, both turns included.
	pulled := res.Get("pulled").Array()
	require.Len(t, pulled, len(replay)+len(live), out)
	for i, ev := range pulled {
		require.EqualValues(t, i+1, ev.Get("seq").Int())
	}
}

func agentEventStates(events []gjson.Result) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Get("state").String())
	}
	return out
}
