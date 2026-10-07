package core

// Tests for how a bound tool object's state is recorded when a tool returns
// the object's own type, observed from the outside: what a conversation
// restored on another engine reads back, what that restore runs there, and
// whether a tool call that changes nothing moves the state.

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/engineconn"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/internal/buildkit/identity"
)

// toolStateWorkspace is a workspace with one Dang module, served from
// modules/<name> with the given constructor settings (TOML lines), that
// depends on a fresh runlog module (see runLogDang).
func toolStateWorkspace(c *dagger.Client, name, source string, settings ...string) *dagger.Workspace {
	config := fmt.Sprintf("[modules.%s]\nsource = \"modules/%s\"\n", name, name)
	if len(settings) > 0 {
		config += fmt.Sprintf("\n[modules.%s.settings]\n%s\n", name, strings.Join(settings, "\n"))
	}
	return c.Directory().
		WithNewFile("dagger.toml", config).
		WithNewFile("modules/"+name+"/dagger.json", fmt.Sprintf(`{"name":%q,"engineVersion":"v1.0.0-0","sdk":"dang","dependencies":[{"name":"runlog","source":"../runlog"}]}`, name)).
		WithNewFile("modules/"+name+"/main.dang", source).
		WithNewFile("modules/runlog/dagger.json", `{"name":"runlog","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile("modules/runlog/main.dang", fmt.Sprintf(`
type Runlog {
  @cache(policy: FunctionCachePolicy.Never)
  record(label: String!): String! {
    container.from(%[2]q)
      .withMountedCache("/runs", cacheVolume(%[1]q))
      .withEnvVariable("CACHEBUST", UUID.v7)
      .withExec(["sh", "-ec", "echo $1 >> /runs/log; echo $1", "sh", label])
      .stdout
  }

  @cache(policy: FunctionCachePolicy.Never)
  runs: String! {
    let out = container.from(%[2]q)
      .withMountedCache("/runs", cacheVolume(%[1]q))
      .withEnvVariable("CACHEBUST", UUID.v7)
      .withExec(["sh", "-c", "paste -sd, /runs/log 2>/dev/null || true"])
      .stdout
    "runs: [" + out.trimSpace + "]"
  }
}
`, "tool-state-runs-"+identity.NewID(), alpineImage)).
		AsWorkspace()
}

// runLogDang gives a module type a log of the producers that ran on the
// current engine: logRun(label) appends label to a cache volume, and the runs
// tool reads the volume back as "runs: [a,b]". Cache volumes are engine
// state, so on a fresh engine the log starts empty and lists exactly what ran
// there. The log lives in a dependency, so a module revision changed by a
// tool reload still shares it with the revision before.
const runLogDang = `
  let logRun(label: String!): String! {
    runlog.record(label: label)
  }

  @cache(policy: FunctionCachePolicy.Never)
  runs: String! {
    runlog.runs
  }
`

// lastRuns returns the producers the last runs tool call listed.
func lastRuns(t *testctx.T, transcript string) []string {
	t.Helper()
	matches := regexp.MustCompile(`runs: \[([^\]]*)\]`).FindAllStringSubmatch(transcript, -1)
	require.NotEmpty(t, matches, transcript)
	runs := matches[len(matches)-1][1]
	if runs == "" {
		return []string{}
	}
	return strings.Split(runs, ",")
}

// stamps returns, in order, every stamp the stamp tool printed. The tool is
// cached on its receiver, so two calls print the same stamp exactly when they
// ran on the same bound state.
func stamps(transcript string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`stamp=([0-9a-f-]{36})`).FindAllStringSubmatch(transcript, -1) {
		out = append(out, m[1])
	}
	return out
}

const stampDang = `
  """Print a stamp minted once per bound state."""
  stamp: String! { "stamp=" + UUID.v7 }
`

// coldEngine starts a dev engine with its own, empty state and connects to
// it. Loading a recipe there can't hit anything the producing engine cached,
// so whatever the recipe still contains runs again: the place to restore a
// conversation to find out what restoring it costs.
func coldEngine(ctx context.Context, t *testctx.T, env ...string) *dagger.Client {
	t.Helper()
	host := connect(ctx, t)
	_, _, endpoint := startArchiveEngine(ctx, t, host, devEngineContainer(host))
	c, _ := connectWithTrace(ctx, t, engineconn.Config{RunnerHost: endpoint, ExtraEnv: env})
	return c
}

// restoreOnColdEngine captures result's committed recipe, ends the producing
// session, and loads the recipe on a fresh engine.
func restoreOnColdEngine(ctx context.Context, t *testctx.T, c *dagger.Client, sink *agentTraceSink, result *dagger.LLM, env ...string) *dagger.LLM {
	t.Helper()
	recipe, err := sink.captureLLMRecipe(ctx, t, c, result)
	require.NoError(t, err)
	require.NoError(t, c.Close())
	return dagger.Ref[*dagger.LLM](coldEngine(ctx, t, env...), recipe)
}

// TestToolStateRestoreSkipsProducer covers a @cache(Never) method returning
// its own type: restoring the conversation on another engine and dispatching
// tools on the restored state reads the same state without running the
// method again.
func (LLMSuite) TestToolStateRestoreSkipsProducer(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	ws := toolStateWorkspace(c, "counter", `
type Counter {
  let count: Int! = 0
  let last: Directory! = directory

  agent(base: LLM!): LLM! @agent {
    base.withTools(currentNode)
  }

  @cache(policy: FunctionCachePolicy.Never)
  bump: Counter! {
    logRun("bump")
    count += 1
    self.last = directory.withNewFile("count", toString(count))
    self
  }

  @cache(policy: FunctionCachePolicy.Never)
  noop: Counter! {
    self
  }

  readCount: String! {
    "count: " + toString(count) + "; file: " + last.file("count").contents
  }
`+runLogDang+`}
`)
	script := recomposeRecordingTurn(c.LLM(), "bump", "bump", "bump", "noop", "readCount", "runs")
	script = recomposeRecordingTurn(script, "read", "readCount", "runs")
	model := cannedRecordingModel(ctx, t, c, script)
	composed, err := composeArtifactAgents(ctx, c, ws, nil, c.LLM(dagger.LLMOpts{Model: model}).WithWorkspace(ws))
	require.NoError(t, err)
	result := composed.WithPrompt("bump").Loop()
	transcript, err := result.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "count: 2; file: 2")
	require.Equal(t, []string{"bump", "bump"}, lastRuns(t, transcript), "the run log records each bump")

	restored := restoreOnColdEngine(ctx, t, c, sink, result).WithPrompt("read").Loop()
	transcript, err = restored.Transcript(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(transcript, "count: 2; file: 2"), transcript)
	require.Empty(t, lastRuns(t, transcript), "restoring the state must not run bump again")
}

// historyDang is a module whose advance tool overwrites one field with an
// object a @cache(Never) function produced, logging each production. The
// object is a module object, so its identity is the call that produced it:
// loading it anywhere that call is not cached runs the call again. (A core
// object a module function returns, like a Directory, is identified by its
// own pure recipe instead, and would not show a replay.)
const historyDang = `
type Snapshot {
  label: String!
}

type History {
  let step: Int! = 0
  let current: Dagger.HistorySnapshot = null
%s
  agent(base: LLM!): LLM! @agent {
    base.withTools(currentNode)
  }

  @cache(policy: FunctionCachePolicy.Never)
  produce(label: String!): Snapshot! {
    logRun(label)
    Snapshot(label: label)
  }

  @cache(policy: FunctionCachePolicy.Never)
  advance: History! {
    step += 1
    self.current = history.produce(label: "v" + toString(step))
    self
  }

  read: String! {
    let cur = current
    "step: " + toString(step) + "; current: " + (if (cur == null) { "none" } else { cur.label })
  }
%s}
`

// TestToolStateRestoreDropsOverwrittenValues covers a field overwritten
// several times, each time with a value another @cache(Never) call produced.
// The restored state holds only the current value: restoring it on another
// engine may run the current value's producer (a reference keeps its own
// recipe), but never the producers of values the conversation overwrote.
func (LLMSuite) TestToolStateRestoreDropsOverwrittenValues(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	ws := toolStateWorkspace(c, "history", fmt.Sprintf(historyDang, "", runLogDang))
	script := recomposeRecordingTurn(c.LLM(), "advance", "advance", "advance", "advance", "read", "runs")
	script = recomposeRecordingTurn(script, "read", "read", "runs")
	model := cannedRecordingModel(ctx, t, c, script)
	composed, err := composeArtifactAgents(ctx, c, ws, nil, c.LLM(dagger.LLMOpts{Model: model}).WithWorkspace(ws))
	require.NoError(t, err)
	result := composed.WithPrompt("advance").Loop()
	transcript, err := result.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "step: 3; current: v3")
	require.Equal(t, []string{"v1", "v2", "v3"}, lastRuns(t, transcript), "the run log records each production")

	restored := restoreOnColdEngine(ctx, t, c, sink, result).WithPrompt("read").Loop()
	transcript, err = restored.Transcript(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(transcript, "step: 3; current: v3"), transcript)
	runs := lastRuns(t, transcript)
	require.NotContains(t, runs, "v1", "an overwritten value's producer must not run on restore")
	require.NotContains(t, runs, "v2", "an overwritten value's producer must not run on restore")
	require.Subset(t, []string{"v3"}, runs)
}

// TestToolStateReloadDropsHistory covers a tool reload on top of recorded
// state: the reloaded state, changed again and restored on another engine,
// reads back the state carried across the reload plus the later change, and
// the restore runs none of the producers of values overwritten before it. The
// new revision also drops a public field the old state held.
func (LLMSuite) TestToolStateReloadDropsHistory(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	initial := fmt.Sprintf(historyDang, `
  legacy: String! = "legacy"
`, runLogDang)
	updated := fmt.Sprintf(historyDang, "", runLogDang+`
  added: String! { "added; step: " + toString(step) }
`)
	ws := toolStateWorkspace(c, "history", initial)
	script := recomposeRecordingTurn(c.LLM(), "before", "advance", "advance", "read")
	script = recomposeRecordingTurn(script, "after", "added", "advance", "read", "runs")
	script = recomposeRecordingTurn(script, "restored", "read", "added", "runs")
	model := cannedRecordingModel(ctx, t, c, script)
	llm, err := composeArtifactAgents(ctx, c, ws, nil, c.LLM(dagger.LLMOpts{Model: model}).WithWorkspace(ws))
	require.NoError(t, err)
	llm = llm.WithPrompt("before").Loop()
	transcript, err := llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "step: 2; current: v2")

	llm, err = recomposeLLM(ctx, c, ws.WithNewFile("modules/history/main.dang", updated), llm)
	require.NoError(t, err)
	llm = llm.WithPrompt("after").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "added; step: 2", "the reload carries the state over")
	require.Contains(t, transcript, "step: 3; current: v3")
	require.Equal(t, []string{"v1", "v2", "v3"}, lastRuns(t, transcript))

	restored := restoreOnColdEngine(ctx, t, c, sink, llm).WithPrompt("restored").Loop()
	transcript, err = restored.Transcript(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(transcript, "step: 3; current: v3"), transcript)
	require.Contains(t, transcript, "added; step: 3")
	require.NotContains(t, transcript, "is not available")
	runs := lastRuns(t, transcript)
	require.NotContains(t, runs, "v1", "a value overwritten before the reload must not be produced on restore")
	require.NotContains(t, runs, "v2", "a value overwritten before the reload must not be produced on restore")
	require.Subset(t, []string{"v3"}, runs)
}

const toolStateTokenEnv = "TOOL_STATE_TOKEN"

// kindsDang holds a field of every kind a module object's state can hold.
const kindsDang = `
enum Mood {
  CALM
  ANGRY
}

type Item {
  label: String!
  dir: Directory!
}

type Kinds {
  let hidden: String! = "unset"
  mood: Mood! = Mood.CALM
  item: Item! = Item(label: "none", dir: directory.withNewFile("f", "none"))
  items: [Item!]! = []
  let big: Int! = 0
  let meta: JSON = null
  let seed: Secret = null
  let token: Secret = null
  note: String = "initial"

  new(seed: Secret) {
    self.seed = seed
    self
  }

  agent(base: LLM!): LLM! @agent {
    base.withTools(currentNode)
  }

  @cache(policy: FunctionCachePolicy.Never)
  mutate: Kinds! {
    self.hidden = "changed"
    self.mood = Mood.ANGRY
    self.item = Item(label: "one", dir: directory.withNewFile("f", "one"))
    self.items = [
      Item(label: "a", dir: directory.withNewFile("f", "a")),
      Item(label: "b", dir: directory.withNewFile("f", "b")),
    ]
    self.big = 9007199254740993
    self.meta = """{"z": 1.0, "a": [1e3, 0.1, 9007199254740993], "m": {"y": null, "x": "s"}}""" :: JSON!
    self.token = seed
    self.note = null
    self
  }

  @cache(policy: FunctionCachePolicy.Never)
  noop: Kinds! {
    self
  }

  describe: String! {
    let t = token
    let n = note
    let tokenState = if (t == null) { "null" } else { if (t.plaintext == "s3cr3t") { "ok" } else { "wrong" } }
    "describe: hidden=" + hidden +
      " mood=" + toString(mood) +
      " item=" + item.label + ":" + item.dir.file("f").contents +
      " items=" + items.map { i => i.label + ":" + i.dir.file("f").contents }.join(",") +
      " big=" + toString(big) +
      " meta=" + toString(meta) +
      " note=" + (n ?? "null") +
      " token=" + tokenState
  }
` + stampDang + `}
`

// TestToolStateFieldKinds covers every kind of field a module object's state
// holds — private fields, enums, nested module objects holding references,
// lists of them, big integers, JSON holding floats and big numbers, a secret,
// and a nullable field set to null — read back from a state restored on
// another engine. (The secret is the caller's, from an env:// constructor
// setting: a module may not read the caller's environment itself. Float
// fields are left out: the Dang SDK cannot decode them in object state.)
//
// It also covers what must NOT move the state: a tool returning its receiver
// unchanged, after those values have round-tripped through the module and
// through a restore on another engine (where references come back in another
// form). stamp is cached on its receiver, so it prints the same stamp again
// exactly when the bound state did not change.
func (LLMSuite) TestToolStateFieldKinds(ctx context.Context, t *testctx.T) {
	env := []string{toolStateTokenEnv + "=s3cr3t"}
	c, sink := connectWithTrace(ctx, t, engineconn.Config{ExtraEnv: env})
	ws := toolStateWorkspace(c, "kinds", kindsDang, fmt.Sprintf("seed = %q", "env://"+toolStateTokenEnv))
	script := recomposeRecordingTurn(c.LLM(), "mutate", "stamp", "mutate", "stamp", "noop", "stamp", "describe")
	script = recomposeRecordingTurn(script, "restored", "stamp", "noop", "stamp", "describe")
	model := cannedRecordingModel(ctx, t, c, script)
	composed, err := composeArtifactAgents(ctx, c, ws, nil, c.LLM(dagger.LLMOpts{Model: model}).WithWorkspace(ws))
	require.NoError(t, err)
	result := composed.WithPrompt("mutate").Loop()
	transcript, err := result.Transcript(ctx)
	require.NoError(t, err)
	describeLine := regexp.MustCompile(`describe: [^\n]*token=\w+`)
	described := describeLine.FindString(transcript)
	require.NotEmpty(t, described, transcript)
	for _, want := range []string{
		"hidden=changed", "mood=ANGRY", "item=one:one", "items=a:a,b:b",
		"big=9007199254740993", "note=null", "token=ok",
	} {
		require.Contains(t, described, want)
	}
	require.Contains(t, described, "meta=")
	require.NotContains(t, described, "meta=null")
	produced := stamps(transcript)
	require.Len(t, produced, 3, transcript)
	require.NotEqual(t, produced[0], produced[1], "a state change moves the state")
	require.Equal(t, produced[1], produced[2], "an unchanged return must not move the state")

	restored := restoreOnColdEngine(ctx, t, c, sink, result, env...).WithPrompt("restored").Loop()
	transcript, err = restored.Transcript(ctx)
	require.NoError(t, err)
	all := describeLine.FindAllString(transcript, -1)
	require.Len(t, all, 2, transcript)
	require.Equal(t, described, all[1], "the restored state reads back the same")
	restoredStamps := stamps(transcript)
	require.Len(t, restoredStamps, 5, transcript)
	require.Equal(t, restoredStamps[3], restoredStamps[4], "an unchanged return after a restore must not move the state")
}
