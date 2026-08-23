package core

// These tests cover module functions that call Dagger's LLM API. They verify
// direct calls, `dagger shell` argument handling, API limit errors, and the
// `--allow-llm` permission gate.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/dag"
	"github.com/creack/pty"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

/* NOTE: These tests use canned conversations rather than live providers: each
test constructs the exact message history it needs through the LLM API itself
(withPrompt/withResponse/withToolResult), exports it with the same messages
selection a real recording would use, and replays it via a replay/ model (see
cannedReplayModel). Deriving the recording from the engine on every run keeps
it in lockstep with the export/decode format by construction — there are no
stored recordings to go stale, and no API keys are needed. */

type LLMSuite struct{}

func TestLLM(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(LLMSuite{})
}

type LLMTestCase struct {
	Ref   string
	Name  string
	Flags []LLMTestCaseFlag
	// Conversation constructs the canned message history this case replays,
	// through the LLM API itself (no live provider).
	Conversation func(*dagger.Client) *dagger.LLM
}

type LLMTestCaseFlag struct {
	Key      string
	Value    string
	Optional bool
}

const (
	// testModulesVersion pins github.com/dagger/dagger-test-modules, so that
	// changes to that repo take effect only when the pin is bumped instead of
	// immediately affecting every branch's CI. Currently the head of the
	// dang-llm-modules branch.
	testModulesVersion = "4232918aa11c5347758ce657659e92f43610f0ff"

	// llm-direct prompts the LLM in the most minimal way, forked per call
	// (via its cacheBuster argument) to bust caches
	directModuleSymbolic = "github.com/dagger/dagger-test-modules/llm/direct"
	// llm-indirect only reaches the LLM through its dependency on llm-direct
	indirectModuleSymbolic = "github.com/dagger/dagger-test-modules/llm/indirect"

	// pinned refs for loading the modules; the allow-llm policy matches
	// against the unpinned symbolic form
	directModuleRef   = directModuleSymbolic + "@" + testModulesVersion
	indirectModuleRef = indirectModuleSymbolic + "@" + testModulesVersion
)

// llmMessagesSelection selects everything a replay recording needs from a
// conversation: the same JSON shape core.decodeReplayMessages consumes.
const llmMessagesSelection = `role content{kind text callId toolName arguments errored signature} tokenUsage{inputTokens outputTokens cachedTokenReads cachedTokenWrites totalTokens}`

type recordedTokenUsage struct {
	InputTokens       int64 `json:"inputTokens,omitempty"`
	OutputTokens      int64 `json:"outputTokens,omitempty"`
	CachedTokenReads  int64 `json:"cachedTokenReads,omitempty"`
	CachedTokenWrites int64 `json:"cachedTokenWrites,omitempty"`
	TotalTokens       int64 `json:"totalTokens,omitempty"`
}

type recordedBlock struct {
	Kind      string `json:"kind"`
	Text      string `json:"text,omitempty"`
	CallID    string `json:"callId,omitempty"`
	ToolName  string `json:"toolName,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Errored   bool   `json:"errored,omitempty"`
	Signature string `json:"signature,omitempty"`
}

type recordedMessage struct {
	Role       string              `json:"role"`
	Content    []recordedBlock     `json:"content"`
	TokenUsage *recordedTokenUsage `json:"tokenUsage,omitempty"`
}

// messagesGolden extracts the messages array at the given gjson path in a
// `dagger query` result and renders it as a replay recording.
func messagesGolden(t *testctx.T, queryOutput string, path string) []byte {
	t.Helper()
	raw := gjson.Get(queryOutput, path)
	require.True(t, raw.Exists(), "path %q missing in query output:\n%s", path, queryOutput)
	var msgs []recordedMessage
	require.NoError(t, json.Unmarshal([]byte(raw.Raw), &msgs))
	for i := range msgs {
		// drop all-zero usage (prompts, tool results) for readability
		if msgs[i].TokenUsage != nil && *msgs[i].TokenUsage == (recordedTokenUsage{}) {
			msgs[i].TokenUsage = nil
		}
	}
	data, err := json.MarshalIndent(msgs, "", "  ")
	require.NoError(t, err)
	return data
}

// recordMessages runs a raw GraphQL query that drives a conversation and
// renders the messages export at the given gjson path as a replay recording.
// The query should select messages{llmMessagesSelection}.
func recordMessages(t *testctx.T, c *dagger.Client, query string, vars map[string]any, path string) []byte {
	t.Helper()
	var opts *testutil.QueryOptions
	if vars != nil {
		opts = &testutil.QueryOptions{Variables: vars}
	}
	res, err := testutil.QueryWithClient[map[string]any](c, t, query, opts)
	require.NoError(t, err)
	raw, err := json.Marshal(res)
	require.NoError(t, err)
	return messagesGolden(t, string(raw), path)
}

// cannedReplayModel derives a replay/ model from a conversation constructed
// through the LLM API itself (withPrompt/withResponse/withToolResult) — no
// live provider involved. The recording round-trips through the same messages
// export a real conversation would use, so its shape cannot drift from what
// the replay decoder expects: both come from the engine under test.
func cannedReplayModel(ctx context.Context, t *testctx.T, c *dagger.Client, llm *dagger.LLM) string {
	t.Helper()
	llmID, err := llm.ID(ctx)
	require.NoError(t, err)
	recording := recordMessages(t, c,
		fmt.Sprintf(`query($llm: ID!){node(id:$llm){... on LLM{messages{%s}}}}`, llmMessagesSelection),
		map[string]any{"llm": llmID},
		"node.messages")
	return "replay/" + base64.StdEncoding.EncodeToString(recording)
}

func (flag LLMTestCaseFlag) ToCall() []string {
	return []string{"--" + flag.Key, flag.Value}
}

func (flag LLMTestCaseFlag) ToShell() []string {
	if flag.Optional {
		return []string{"--" + flag.Key, strconv.Quote(flag.Value)}
	}
	return []string{strconv.Quote(flag.Value)}
}

func (LLMSuite) TestCase(ctx context.Context, t *testctx.T) {
	tcs := []LLMTestCase{
		{
			Name: "hello-world",
			Ref:  "./llmtest/go-programmer/",
			Flags: []LLMTestCaseFlag{
				{
					Key:   "assignment",
					Value: "write a hello world program",
				},
			},
			// Mirrors the conversation GoProgrammer.drive starts: the first
			// user message must match the module's withPrompt text byte for
			// byte (the replayer diffs TEXT blocks), while tool results are
			// placeholders — the real read/write/build tools run during
			// replay and their live results flow through.
			Conversation: func(c *dagger.Client) *dagger.LLM {
				return c.LLM().
					WithPrompt("You are an expert go programmer. You have access to a workspace.\n"+
						"Use the read, write, build tools to complete the following assignment.\n"+
						"Do not try to access the container directly.\n"+
						"Don't stop until your code builds.\n"+
						"\n"+
						"Assignment: write a hello world program\n").
					WithResponse([]dagger.LLMContentBlockInput{
						{Kind: dagger.LLMContentBlockKindText, Text: "Let me check the current main.go first."},
						{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "read"},
					}).
					WithToolResult("call_1", `workspace file "main.go": stat main.go: no such file or directory`, true).
					WithResponse([]dagger.LLMContentBlockInput{
						{Kind: dagger.LLMContentBlockKindText, Text: "No main.go yet, so I'll write a hello world program."},
						{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_2", ToolName: "write",
							Arguments: dagger.JSON(`{"content":"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"Hello, World!\")\n}\n"}`)},
					}).
					WithToolResult("call_2", "", false).
					WithResponse([]dagger.LLMContentBlockInput{
						{Kind: dagger.LLMContentBlockKindText, Text: "Now let me build it to make sure it compiles."},
						{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_3", ToolName: "build"},
					}).
					WithToolResult("call_3", "", false).
					WithResponse([]dagger.LLMContentBlockInput{
						{Kind: dagger.LLMContentBlockKindText, Text: "Done: main.go builds and prints Hello, World!"},
					})
			},
		},
	}
	for _, tc := range tcs {
		t.Run(tc.Name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			srcPath, err := filepath.Abs(tc.Ref)
			require.NoError(t, err)
			ctr := goGitBase(t, c).
				WithWorkdir("/work").
				WithMountedDirectory(".", c.Host().Directory(srcPath))

			var flags []string
			for _, flag := range tc.Flags {
				flags = append(flags, flag.ToCall()...)
			}

			model := cannedReplayModel(ctx, t, c, tc.Conversation(c))

			t.Run("call", func(ctx context.Context, t *testctx.T) {
				// run drives the replayed conversation and returns the final
				// main.go contents from the LLM's workspace.
				cmd := []string{"--model=" + model, "run"}
				cmd = append(cmd, flags...)
				out, err := ctr.With(daggerCallAt(".", cmd...)).Stdout(ctx)
				require.NoError(t, err)
				testGoProgram(ctx, t, c, dag.Directory().WithNewFile("main.go", out).File("main.go"), regexp.MustCompile("(?i)hello(.*)world"))
			})

			t.Run("shell", func(ctx context.Context, t *testctx.T) {
				var flags []string
				for _, flag := range tc.Flags {
					flags = append(flags, flag.ToShell()...)
				}
				out, err := ctr.
					With(daggerShellAt(".", fmt.Sprintf(`. --model="%s" | run %s`, model, strings.Join(flags, " ")))).
					Stdout(ctx)
				require.NoError(t, err)
				testGoProgram(ctx, t, c, dag.Directory().WithNewFile("main.go", out).File("main.go"), regexp.MustCompile("(?i)hello(.*)world"))
			})
		})
	}
}

// TestGeneratorSeesOverlayEdits locks in that the LLM's bound (overlaid)
// Workspace propagates through a module's `generate` tool into the generator
// leaves it rolls up and runs. Regression test for the rebase break where an
// auto-injected Workspace! on a generator leaf — resolved while running inside
// the module runtime — was rejected by loadWorkspaceArg's
// callerInModuleFunction guard *before* it consulted the seeded bound
// workspace, so the generator read stale (frozen) source and the agent's edit
// had no effect (see hack/designs/workspace-agents.md §4).
//
// The gen-agent fixture's generator reads input.txt and writes
// output.txt = "generated from: <input>". The canned conversation edits
// input.txt to "B-OVERLAY" via the write tool (overlaying the bound
// workspace), then calls the generate tool. run returns output.txt, so if the
// overlay reached the generator it reads "generated from: B-OVERLAY" rather
// than the frozen "generated from: A".
func (LLMSuite) TestGeneratorSeesOverlayEdits(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	srcPath, err := filepath.Abs("./llmtest/gen-agent/")
	require.NoError(t, err)
	// The generator is discovered via Workspace.generators, so the fixture
	// must be a detected workspace: a git root with the dagger.toml the
	// fixture ships. goGitBase already `git init`s /work; copy the fixture in
	// (WithDirectory, so the repo's .git survives) and commit it so detection
	// succeeds.
	ctr := goGitBase(t, c).
		WithWorkdir("/work").
		WithDirectory(".", c.Host().Directory(srcPath)).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"})

	// The write tool overlays input.txt=B-OVERLAY onto the bound workspace,
	// then the generate tool runs the workspace generators against that
	// overlay. The tool results are placeholders — the real write/generate
	// tools run during replay and their live results flow through.
	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt("You are an agent operating on a workspace.\n"+
			"Use the write tool to edit input.txt, then the generate tool to run the workspace generators.\n"+
			"\n"+
			"Assignment: set input.txt to B-OVERLAY and regenerate\n").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Editing input.txt."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "write",
				Arguments: dagger.JSON(`{"content":"B-OVERLAY"}`)},
		}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Now running the generators."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_2", ToolName: "generate"},
		}).
		WithToolResult("call_2", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Done: regenerated output.txt from the edited input.txt."},
		}))

	out, err := ctr.
		With(daggerCall("gen-agent", "--model="+model, "run", "--assignment", "set input.txt to B-OVERLAY and regenerate")).
		Stdout(ctx)
	require.NoError(t, err)
	// The generator observed the overlay edit, not the frozen "A".
	require.Contains(t, out, "generated from: B-OVERLAY")
}

// TestToolLogsExcludeInternal locks in captureLogs' internal-span filtering:
// a tool result surfaces the print output of the tool's real work, but not
// logs from beneath spans marked dagger.io/ui.internal — e.g. ComputePaths'
// "computing paths" task prints (added:/removed:/...), which used to leak
// into Workspace-returning tool results ahead of the patch summary.
func (LLMSuite) TestToolLogsExcludeInternal(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	srcPath, err := filepath.Abs("./llmtest/go-programmer/")
	require.NoError(t, err)
	ctr := goGitBase(t, c).
		WithWorkdir("/work").
		WithMountedDirectory(".", c.Host().Directory(srcPath))

	// Mirrors GoProgrammer.drive's conversation (the first user message must
	// match its withPrompt byte for byte): write main.go, then build it. Tool
	// results are placeholders — the real tools run during replay.
	// Give the workspace a fresh digest so the nested execs actually run. If
	// they hit the shared cache, there is no live stdout for captureLogs to
	// surface and the build result correctly collapses to "(done)".
	source := fmt.Sprintf("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"Hello, World!\")\n}\n\n// cache-buster: %s\n", identity.NewID())
	writeArgs, err := json.Marshal(map[string]string{"content": source})
	require.NoError(t, err)
	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt("You are an expert go programmer. You have access to a workspace.\n"+
			"Use the read, write, build tools to complete the following assignment.\n"+
			"Do not try to access the container directly.\n"+
			"Don't stop until your code builds.\n"+
			"\n"+
			"Assignment: write a hello world program\n").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Writing main.go."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "write",
				Arguments: dagger.JSON(writeArgs)},
		}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Building."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_2", ToolName: "build"},
		}).
		WithToolResult("call_2", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Done."},
		}))

	out, err := ctr.
		With(daggerShellAt(".", fmt.Sprintf(`. --model="%s" | drive "write a hello world program" | loop | transcript`, model))).
		Stdout(ctx)
	require.NoError(t, err)

	// The write tool (Workspace-returning) reports the patch summary...
	require.Contains(t, out, "diff --git")
	// ...without the internal "computing paths" span's prints leaking in.
	require.NotContains(t, out, "added: [")
	// The build tool's real work still surfaces: its exec output sits beneath
	// non-internal spans, so logsOrDone returns it rather than "(done)".
	require.Contains(t, out, "creating new go.mod")
}

// TestToolLogsExcludeService locks in captureLogs' service-span filtering:
// a tool result surfaces the tool's deliberate print output, but not logs
// from long-lived service exec spans (dagger.io/service) — those enter the
// tool-call subtree via cause links (gaining new links per session, e.g.
// when a later tool call reloads the started service) and would otherwise
// drown out the print signal in the 8-line tail. ReadLogs remains the
// deliberate discovery path for service logs.
func (LLMSuite) TestToolLogsExcludeService(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	srcPath, err := filepath.Abs("./llmtest/svc-agent/")
	require.NoError(t, err)
	ctr := goGitBase(t, c).
		WithWorkdir("/work").
		WithMountedDirectory(".", c.Host().Directory(srcPath))

	// Mirrors SvcAgent.drive's conversation (the first user message must
	// match its withPrompt byte for byte): start the noisy service, then
	// stop it. Tool results are placeholders — the real tools run during
	// replay.
	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt("You are an agent that manages a service.\n"+
			"Use the start tool to start the service, then the stop tool to stop it.\n"+
			"\n"+
			"Assignment: start and stop the service\n").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Starting the service."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "start"},
		}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Now stopping the service."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_2", ToolName: "stop"},
		}).
		WithToolResult("call_2", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Done: service started and stopped."},
		}))

	out, err := ctr.
		With(daggerShellAt(".", fmt.Sprintf(`. --model="%s" | drive "start and stop the service" | loop | transcript`, model))).
		Stdout(ctx)
	require.NoError(t, err)

	// The start tool's deliberate print survives into its tool result: the
	// service's 200 noise lines all land before the healthcheck passes, so
	// without service filtering they'd swamp the 8-line tail.
	require.Contains(t, out, "SERVICE-READY")
	// The service exec span's logs stay out of tool results entirely.
	require.NotContains(t, out, "SVC-NOISE")
	// stop's print also surfaces — this exercises the late-cause-link route:
	// stop reloads the started service, re-linking the exec span (and its
	// full log history) beneath the stop tool call.
	require.Contains(t, out, "SERVICE-STOPPED")
}

// TestToolLogsKeepReport locks in that a module function's own print output
// reaches the tool result in full, even after noisy nested work. It covers
// both halves: the Dang runtime routes stdio to the user-facing span (the
// function call the user sees) rather than the passthrough call_exec
// profiling span it currently runs under — as containerized SDKs do via the
// injected traceparent — and captureLogLines then classifies that output as
// the tool's own and keeps it verbatim, abridging only nested work.
func (LLMSuite) TestToolLogsKeepReport(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	srcPath, err := filepath.Abs("./llmtest/report-agent/")
	require.NoError(t, err)
	ctr := goGitBase(t, c).
		WithWorkdir("/work").
		WithMountedDirectory(".", c.Host().Directory(srcPath))

	// Mirrors ReportAgent.drive's conversation (the first user message must
	// match its withPrompt byte for byte). Tool results are placeholders —
	// the real tool runs during replay.
	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt("You are an agent that writes a report.\n"+
			"Use the report tool to do the work and write the report.\n"+
			"\n"+
			"Assignment: do the work and write the report\n").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Doing the work."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "report"},
		}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Done: the report is written."},
		}))

	out, err := ctr.
		With(daggerShellAt(".", fmt.Sprintf(`. --model="%s" | drive "do the work and write the report" | loop | transcript`, model))).
		Stdout(ctx)
	require.NoError(t, err)

	// Every line of the tool's own report survives into the tool result.
	for i := 1; i <= 14; i++ {
		require.Contains(t, out, fmt.Sprintf("LINE-%02d", i))
	}
	// The nested exec's output is still abridged to its trailing lines.
	require.NotContains(t, out, "NESTED-NOISE-01")
	require.Contains(t, out, "NESTED-NOISE-20")
}

func (LLMSuite) TestStepLimit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// maxSteps is a loop() argument: the limit caps the loop invocation
	// rather than the LLM as a whole. Binding a container's methods as tools
	// gives the recorded conversation a tool call, so the loop needs a second
	// API call and trips the limit.
	ctrFn := func(llmFlags, loopFlags string) dagger.WithContainerFunc {
		return daggerShell(fmt.Sprintf(`llm %s | with-tools $(container | from alpine) | with-prompt "tell me the value of PATH" | loop %s | with-prompt "now tell me the value of TERM" | transcript`, llmFlags, loopFlags))
	}

	// One tool-call turn: step 1 answers with the envVariable call (which
	// really dispatches against the bound alpine container), leaving its
	// result pending, so a --max-steps=1 loop trips the limit before the
	// closing text turn.
	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt("tell me the value of PATH").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindThinking, Text: "Retrieving the PATH environment variable."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "envVariable",
				Arguments: dagger.JSON(`{"name":"PATH"}`)},
		}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "The value of PATH is /usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin."},
		}))
	llmFlags := fmt.Sprintf("--model=%q", model)

	_, err := daggerCliBase(t, c).
		With(ctrFn(llmFlags, "--max-steps=1")).
		Stdout(ctx)
	requireErrOut(t, err, "reached step limit: 1")
}

func (LLMSuite) TestAllowLLM(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// A canned conversation shared amongst subtests: they all drive the same
	// "greet me" prompt through the llm/direct module.
	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt("greet me").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Hello! How can I help you today?"},
		}))
	modelFlag := "--model=" + model

	t.Run("allowed calls", func(ctx context.Context, t *testctx.T) {
		tcs := []struct {
			name     string
			module   string
			allowLLM string
		}{
			{
				name:     "direct allow all",
				module:   directModuleRef,
				allowLLM: "all",
			},
			{
				name:     "direct allow specific module",
				module:   directModuleRef,
				allowLLM: directModuleSymbolic,
			},
			{
				name:     "indirect allow all",
				module:   indirectModuleRef,
				allowLLM: "all",
			},
			{
				name:     "indirect allow specific module",
				module:   indirectModuleRef,
				allowLLM: directModuleSymbolic,
			},
			// we only test various permutations of remote module LLM use, local modules don't require the flag and that's covered by the toy-programmer case
		}

		for _, tc := range tcs {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				args := []string{"--allow-llm", tc.allowLLM, modelFlag, "prompt", "--string-arg", "greet me", "--cache-buster", identity.NewID()}

				_, err := daggerCliBase(t, c).
					With(daggerCallAt(tc.module, args...)).
					Stdout(ctx)
				require.NoError(t, err)
			})
		}
	})

	t.Run("noninteractive prompt fail", func(ctx context.Context, t *testctx.T) {
		args := []string{modelFlag, "prompt", "--string-arg", t.Name(), "--cache-buster", identity.NewID()}

		_, err := daggerCliBase(t, c).
			With(daggerCallAt(directModuleRef, args...)).
			Stdout(ctx)
		require.Error(t, err)
	})

	t.Run("environment variable", func(ctx context.Context, t *testctx.T) {
		_, err := daggerCliBase(t, c).
			WithEnvVariable("DAGGER_ALLOW_LLM", "all").
			With(daggerCallAt(indirectModuleRef, modelFlag, "prompt", "--string-arg", "greet me", "--cache-buster", identity.NewID())).
			Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("shell allow all", func(ctx context.Context, t *testctx.T) {
		_, err := daggerCliBase(t, c).
			WithExec([]string{"dagger", "shell", "-m", indirectModuleRef, "--allow-llm=all"}, dagger.ContainerWithExecOpts{
				Stdin:                         fmt.Sprintf(`. %s | prompt "greet me" %q`, modelFlag, identity.NewID()),
				ExperimentalPrivilegedNesting: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("shell interactive module loads", func(ctx context.Context, t *testctx.T) {
		_, err := daggerCliBase(t, c).
			WithExec([]string{"dagger", "shell", "--allow-llm", directModuleSymbolic}, dagger.ContainerWithExecOpts{
				Stdin:                         fmt.Sprintf(`%s %s | prompt "greet me" %q`, indirectModuleRef, modelFlag, identity.NewID()),
				ExperimentalPrivilegedNesting: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("prompt calls", func(ctx context.Context, t *testctx.T) {
		consoleDagger := func(ctx context.Context, t *testctx.T, args ...string) (*exec.Cmd, *tuiConsole) {
			t.Helper()
			console, err := newTUIConsole(t, 60*time.Second)
			require.NoError(t, err)

			tty := console.Tty()
			err = pty.Setsize(tty, &pty.Winsize{Rows: 10, Cols: 80}) // for plain, we should make this wider, like 150
			require.NoError(t, err)

			cmd := hostDaggerCommand(
				ctx,
				t,
				t.TempDir(),
				args...,
			)
			cmd.Stdin = tty
			cmd.Stdout = tty
			cmd.Stderr = tty

			return cmd, console
		}

		tcs := []struct {
			name     string
			allowLLM string
			module   string
			plain    bool
		}{
			{
				name:     "direct remote module call",
				allowLLM: "",
				module:   directModuleRef,
			},
			// TODO: find a way to test plain tui.
			// under test, it doesn't acknowledge input, but works fine irl
			// {
			// 	name:     "plain tui direct remote module call",
			// 	allowLLM: "",
			// 	module:   directModuleRef,
			// 	plain:    true,
			// },
			{
				name:     "allowed unrelated, calling direct",
				allowLLM: "github.com/dagger/dagger",
				module:   directModuleRef,
			},
			{
				name:     "allowed indirect, calling direct",
				allowLLM: indirectModuleSymbolic,
				module:   directModuleRef,
			},
			{
				// this should prompt for the dependency
				name:     "allowed indirect, calling indirect",
				allowLLM: indirectModuleSymbolic,
				module:   indirectModuleRef,
			},
		}

		for i, tc := range tcs {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				progressFlag := "--progress=auto"
				if tc.plain {
					progressFlag = "--progress=plain"
				}
				cmd, console := consoleDagger(
					ctx, t,
					progressFlag, "call", "-m", tc.module, "--allow-llm", tc.allowLLM, modelFlag, "prompt", "--string-arg", fmt.Sprintf("greet me %d", i), "--cache-buster", identity.NewID(),
				)
				defer console.Close()

				err := cmd.Start()
				require.NoError(t, err)

				_, err = console.ExpectString("Allow LLM access?")
				require.NoError(t, err)

				// only test the  "no" case- the yes case persists history and requires special handling
				_, err = console.SendLine("n")
				require.NoError(t, err)

				_, err = console.ExpectString("was denied LLM access")
				require.NoError(t, err)

				go console.ExpectEOF()

				err = cmd.Wait()
				require.Error(t, err)
			})
		}
	})
}

func testGoProgram(ctx context.Context, t *testctx.T, c *dagger.Client, program *dagger.File, re any) {
	name, err := program.Name(ctx)
	require.NoError(t, err)
	out, err := goGitBase(t, c).
		WithWorkdir("/src").
		WithMountedFile(name, program).
		WithExec([]string{"go", "run", name}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Regexp(t, re, out)
}

// TestPortableID verifies that llm.portableID returns a portable,
// recipe-form ID that node() can resolve in any session, whereas llm.id
// returns an engine-local runtime handle. `dagger llm` session save/resume
// persists portableID; persisting id used to fail on resume with "missing
// shared result" once the original engine was gone.
func (LLMSuite) TestPortableID(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	llm := c.LLM().
		WithModel("openai/gpt-4o").
		WithSystemPrompt("you are a helpful assistant").
		WithPrompt("hello")

	portableID, err := llm.PortableID(ctx)
	require.NoError(t, err)
	handleID, err := llm.ID(ctx)
	require.NoError(t, err)

	// portableID must be a self-contained recipe, not an engine-local handle.
	gid := new(call.ID)
	require.NoError(t, gid.Decode(string(portableID)))
	require.False(t, gid.IsHandle(), "portableID must be recipe-form, got a runtime handle")

	// id is the runtime handle that does not survive across engines: this is
	// exactly the engineResult(N) reference that broke session resume.
	hid := new(call.ID)
	require.NoError(t, hid.Decode(string(handleID)))
	require.True(t, hid.IsHandle(), "id is expected to be a runtime handle")

	// portableID resolves via node() and reconstructs the same conversation.
	reloaded := dagger.Ref[*dagger.LLM](c, portableID)
	reloadedModel, err := reloaded.Model(ctx)
	require.NoError(t, err)
	origModel, err := llm.Model(ctx)
	require.NoError(t, err)
	require.Equal(t, origModel, reloadedModel)
}

// TestPortableIDWithResponse verifies that a conversation containing
// assistant content blocks survives the portableID round trip. Empty
// "arguments" on a
// non-tool-call block decodes to nil and is dropped from the serialized ID
// literal; reloading used to fail with `missing required input field
// "arguments"`, which broke resume for every saved session with a reply.
func (LLMSuite) TestPortableIDWithResponse(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	llm := c.LLM().
		WithModel("openai/gpt-4o").
		WithPrompt("hello").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "hello world"},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "read", Arguments: dagger.JSON(`{"path":"/x"}`)},
		})

	portableID, err := llm.PortableID(ctx)
	require.NoError(t, err)

	reloaded := dagger.Ref[*dagger.LLM](c, portableID)
	reply, err := reloaded.LastReply(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello world", reply)
}

// TestPortableIDCarriesProvider verifies that an explicitly selected provider
// survives the portableID round trip. The model name here matches no known
// provider pattern — the exact case llm(provider:) exists for — so a resumed
// session that re-inferred the provider from the name would route to the
// generic OpenAI-compatible fallback instead of the provider the session was
// created with.
func (LLMSuite) TestPortableIDCarriesProvider(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	llm := c.LLM(dagger.LLMOpts{
		Model:    "my-custom-finetune",
		Provider: "openai",
	}).
		WithPrompt("hello").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "hello world"},
		})

	origProvider, err := llm.Provider(ctx)
	require.NoError(t, err)
	require.Equal(t, "openai", origProvider)

	portableID, err := llm.PortableID(ctx)
	require.NoError(t, err)

	reloaded := dagger.Ref[*dagger.LLM](c, portableID)
	reloadedProvider, err := reloaded.Provider(ctx)
	require.NoError(t, err)
	require.Equal(t, "openai", reloadedProvider,
		"an explicit provider must survive a save/resume round trip")

	reloadedModel, err := reloaded.Model(ctx)
	require.NoError(t, err)
	require.Equal(t, "my-custom-finetune", reloadedModel)
}

// TestDefaultModelPinnedInID verifies that llm() with no model re-calls
// itself with the configured default model and its provider pinned as
// explicit arguments — the Container.from digest-expansion pattern — so the
// recorded ID names the model the conversation actually runs against. A
// saved session then resumes on its own model instead of whatever default
// the resuming environment happens to configure. Runs the CLI in a container
// so the client environment (which the router reads its config from) is
// controlled regardless of the test host's own provider configuration.
func (LLMSuite) TestDefaultModelPinnedInID(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := workspaceBase(t, c).
		WithEnvVariable("OPENAI_MODEL", "gpt-4o-test").
		With(daggerShell(`llm | portable-id`)).
		Stdout(ctx)
	require.NoError(t, err)

	gid := new(call.ID)
	require.NoError(t, gid.Decode(strings.TrimSpace(out)))

	var llmCall *call.ID
	for cur := gid; cur != nil; cur = cur.Receiver() {
		if cur.Field() == "llm" {
			llmCall = cur
		}
	}
	require.NotNil(t, llmCall, "the portable ID must be rooted at llm()")
	pinned := map[string]string{}
	for _, arg := range llmCall.Args() {
		if lit, ok := arg.Value().(*call.LiteralString); ok {
			pinned[arg.Name()] = lit.Value()
		}
	}
	require.Equal(t, "gpt-4o-test", pinned["model"],
		"llm() must pin the configured default model into the recorded call")
	require.Equal(t, "openai", pinned["provider"],
		"llm() must pin the default model's routed provider alongside it")
}

// TestPortableIDDropsSupersededWorkspaceBindings verifies that portableID
// re-emits the session as a flat, data-only recipe: the conversation survives
// byte-for-byte, but the workspace overlays recorded during the session
// (withWorkspace nodes carrying withChanges derivations) are superseded by the
// current binding and dropped, so a persisted ID no longer replays workspace
// edits when loaded. This is what makes ctrl+s (export + rebind) durable:
// replaying an edit chain against already-updated files fails with "search
// string not found" or silently re-applies.
func (LLMSuite) TestPortableIDDropsSupersededWorkspaceBindings(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	c := connect(ctx, t, dagger.WithWorkdir(workdir))
	current := c.CurrentWorkspace()

	// llm() starts unbound; bind the live workspace explicitly, as the CLI
	// does at session start.
	llm := c.LLM().
		WithWorkspace(current).
		WithModel("openai/gpt-4o").
		WithSystemPrompt("be helpful").
		WithPrompt("hello").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "hello world"},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "read", Arguments: dagger.JSON(`{"path":"/x"}`)},
		}).
		WithToolResult("call_1", "file contents", false)

	// Overlay a changeset onto the LLM's workspace, mimicking what a
	// workspace-mutating tool call records mid-session.
	base := c.Directory().WithNewFile("a.txt", "before")
	edited := base.WithNewFile("a.txt", "after")
	llmEdited := llm.WithWorkspace(llm.Workspace().WithChanges(edited.Changes(base)))

	// Simulate ctrl+s after the export: rebind the live workspace, whose
	// on-disk content the export just made equal to the overlay result.
	rebound := llmEdited.WithWorkspace(current)

	// The conversation is preserved exactly.
	origHist, err := llmEdited.Transcript(ctx)
	require.NoError(t, err)
	reboundHist, err := rebound.Transcript(ctx)
	require.NoError(t, err)
	require.Equal(t, origHist, reboundHist)

	// The persisted recipe is flat: exactly one workspace binding (the current
	// one) survives, and no withResetWorkspace node exists at all.
	globalID, err := rebound.PortableID(ctx)
	require.NoError(t, err)
	gid := new(call.ID)
	require.NoError(t, gid.Decode(string(globalID)))
	var bindings int
	for cur := gid; cur != nil; cur = cur.Receiver() {
		require.NotEqual(t, "withResetWorkspace", cur.Field(),
			"withResetWorkspace is gone; portableID re-emits the recipe itself")
		if cur.Field() == "withWorkspace" {
			bindings++
		}
	}
	require.Equal(t, 1, bindings,
		"only the current workspace binding belongs in the recipe; "+
			"superseded overlay bindings must be dropped")

	// The property that actually matters: reloading the persisted session does
	// not resurrect the already-exported overlay as a pending change.
	reloaded := dagger.Ref[*dagger.LLM](c, globalID)
	reloadedEmpty, err := reloaded.Workspace().Changes(dagger.WorkspaceChangesOpts{From: current}).IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, reloadedEmpty,
		"a reloaded session must not replay already-exported workspace edits")

	// The reloaded session reloads with the conversation intact.
	reply, err := reloaded.LastReply(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello world", reply)
	reloadedHist, err := reloaded.Transcript(ctx)
	require.NoError(t, err)
	require.Equal(t, origHist, reloadedHist)
}

// TestPortableIDDropsNonChangesOverlays verifies that the flattening drops
// workspace overlays applied through mutators other than withChanges — e.g.
// the withNewFile / withNewDirectory calls the built-in filesystem tools use.
// An earlier reset only peeled a trailing withChanges chain, so a workspace
// edited via withNewFile stayed pinned with its overlay: the persisted session
// still reported the (already-exported) edit as a pending change, which is
// what made `dagger agent`'s ctrl+s leave a stale "Changes" bubble and re-diff
// already-saved files as deletions on the next turn. Emitting only the current
// binding strips every overlay shape, including ones added later.
func (LLMSuite) TestPortableIDDropsNonChangesOverlays(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	c := connect(ctx, t, dagger.WithWorkdir(workdir))
	current := c.CurrentWorkspace()

	llm := c.LLM().
		WithWorkspace(current).
		WithModel("openai/gpt-4o").
		WithSystemPrompt("be helpful").
		WithPrompt("hello").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "hello world"},
		})

	// Overlay edits via workspace mutators (not withChanges), mimicking the
	// built-in write tool: currentWorkspace().withNewFile(...).withNewFile(...).
	edited := llm.WithWorkspace(
		llm.Workspace().
			WithNewFile("added.txt", "one").
			WithNewFile("another.txt", "two"),
	)

	// Sanity check: before the rebind the overlay reports the edits as pending.
	editedEmpty, err := edited.Workspace().Changes(dagger.WorkspaceChangesOpts{From: current}).IsEmpty(ctx)
	require.NoError(t, err)
	require.False(t, editedEmpty, "overlaid workspace should report pending changes")

	// Rebind the live workspace, as the CLI does after ctrl+s exports.
	rebound := edited.WithWorkspace(current)
	reboundEmpty, err := rebound.Workspace().Changes(dagger.WorkspaceChangesOpts{From: current}).IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, reboundEmpty,
		"rebinding the live workspace must drop the overlay edits")

	// The persisted recipe carries only the current binding: no overlay
	// mutator survives on the workspace argument's chain.
	globalID, err := rebound.PortableID(ctx)
	require.NoError(t, err)
	gid := new(call.ID)
	require.NoError(t, gid.Decode(string(globalID)))
	for cur := gid; cur != nil; cur = cur.Receiver() {
		require.NotEqual(t, "withResetWorkspace", cur.Field(),
			"withResetWorkspace is gone; portableID re-emits the recipe itself")
		require.NotEqual(t, "withNewFile", cur.Field(),
			"superseded overlay mutators must not reach the persisted recipe")
	}

	// Reloading must not resurrect the already-exported edits.
	reloaded := dagger.Ref[*dagger.LLM](c, globalID)
	reloadedEmpty, err := reloaded.Workspace().Changes(dagger.WorkspaceChangesOpts{From: current}).IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, reloadedEmpty,
		"a reloaded session must not replay already-exported workspace edits")

	// The conversation survives the rebind byte-for-byte.
	origHist, err := edited.Transcript(ctx)
	require.NoError(t, err)
	reboundHist, err := rebound.Transcript(ctx)
	require.NoError(t, err)
	require.Equal(t, origHist, reboundHist)
}

// TestPortableIDPreservesPendingEdits guards the other half of the contract:
// a mid-session autosave (no export, no rebind) must bring the agent's pending
// workspace edits back when the session is resumed. The current binding is
// emitted verbatim, overlay derivations and all, so un-exported work is not
// silently discarded by saving.
func (LLMSuite) TestPortableIDPreservesPendingEdits(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	c := connect(ctx, t, dagger.WithWorkdir(workdir))
	current := c.CurrentWorkspace()

	llm := c.LLM().
		WithWorkspace(current).
		WithModel("openai/gpt-4o").
		WithPrompt("hello").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "hello world"},
		})

	// A tool call edits the workspace; nothing is exported.
	edited := llm.WithWorkspace(llm.Workspace().WithNewFile("pending.txt", "PENDING"))

	// Autosave as-is — the shape LLMSession.AutoSaveSession persists.
	savedID, err := edited.PortableID(ctx)
	require.NoError(t, err)

	reloaded := dagger.Ref[*dagger.LLM](c, savedID)

	// The pending edit comes back, both as content and as a pending change.
	contents, err := reloaded.Workspace().File("pending.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "PENDING", contents)

	reloadedEmpty, err := reloaded.Workspace().Changes(dagger.WorkspaceChangesOpts{From: current}).IsEmpty(ctx)
	require.NoError(t, err)
	require.False(t, reloadedEmpty,
		"un-exported edits must survive a save/resume round trip")

	// And so does the conversation.
	origHist, err := edited.Transcript(ctx)
	require.NoError(t, err)
	reloadedHist, err := reloaded.Transcript(ctx)
	require.NoError(t, err)
	require.Equal(t, origHist, reloadedHist)
}

// TestExportBustsStaleHostReads verifies that Workspace.export invalidates the
// session's cached host reads, so an agent that saves its changes to disk
// (ctrl+s: export then rebind) observes the saved content on its next read
// instead of a stale snapshot cached earlier in the same session.
//
// Host-backed workspace reads (Workspace.file) resolve through host.directory,
// which is cached per client for the client's whole lifetime. Within a single
// long-lived `dagger agent` session that meant a file read early in the
// conversation kept returning its original contents even after the agent's
// edits were exported to disk. Export bumps the client's workspace read epoch
// for exactly this reason, so the read after it must observe the exported
// "NEW" contents rather than the "OLD" ones cached by the earlier read.
func (LLMSuite) TestExportBustsStaleHostReads(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "x.txt"), []byte("OLD"), 0o644))

	c := connect(ctx, t, dagger.WithWorkdir(workdir))

	// Prime the per-client host.directory cache with the original contents,
	// exactly as the agent reading the file before editing it would.
	before, err := c.CurrentWorkspace().File("x.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "OLD", before)

	// Save: export the edited contents to the local Git workspace on disk, as
	// ctrl+s does before rebinding.
	require.NoError(t, c.CurrentWorkspace().WithNewFile("x.txt", "NEW").Export(ctx))

	// Rebind the live workspace, as the CLI does after ctrl+s. The file on
	// disk now holds "NEW".
	llm := c.LLM().WithWorkspace(c.CurrentWorkspace())

	// The next read must observe the exported contents, not the snapshot the
	// earlier read cached for the session.
	after, err := llm.Workspace().File("x.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "NEW", after)
}

// TestWorkspaceReloaded covers the ctrl+u direction: the agent discards its
// pending overlay to re-sync with whatever is on disk now. Nothing was
// exported, so no epoch bump happened on its own — Workspace.reloaded is what
// invalidates the session's cached host reads, letting the agent see edits the
// user made outside the session.
func (LLMSuite) TestWorkspaceReloaded(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "x.txt"), []byte("OLD"), 0o644))

	c := connect(ctx, t, dagger.WithWorkdir(workdir))

	// Prime the per-client host.directory cache.
	before, err := c.CurrentWorkspace().File("x.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "OLD", before)

	// The user edits the file outside the session — no export, so nothing
	// invalidates the cached read on its own.
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "x.txt"), []byte("NEW"), 0o644))

	// Reloading the workspace busts the cache, so the agent re-reads the host.
	after, err := c.LLM().
		WithWorkspace(c.CurrentWorkspace().Reloaded()).
		Workspace().File("x.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "NEW", after)
}

// TestNestedClientInheritsSessionConfig verifies that LLM configuration is
// session-wide: a nested client (an experimentalPrivilegedNesting exec, e.g.
// `dagger agent` running inside a container) with no LLM configuration of its
// own inherits the session main client's config; its own config still wins
// where set; and the underlying credentials never become readable from inside
// the nested container — the env:// lookups resolve through the *main*
// client's session, so nesting grants use of the LLM, not the keys.
func (LLMSuite) TestNestedClientInheritsSessionConfig(ctx context.Context, t *testctx.T) {
	// Config on the session's main client (this test process): the router
	// resolves env:// through the client's session attachables at load time,
	// so a plain os.Setenv is all it takes (same pattern as
	// AddressSuite/TestSecret). The values are inert for other tests: replay
	// models ignore credentials, and an anthropic model only routes when
	// nothing higher-priority is configured.
	sessionModel := "claude-session-wide-model"
	apiKey := "secret" + identity.NewID()
	os.Setenv("ANTHROPIC_MODEL", sessionModel)
	os.Setenv("ANTHROPIC_API_KEY", apiKey)

	// Each subtest connects on its own: LLM settings resolve once per session
	// (see TestCrossSessionLLM), so a shared session would serve the first
	// subtest's model to the others from the session cache.

	t.Run("inherits main client config", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := goGitBase(t, c).
			With(daggerExecRaw("core", "llm", "model")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, sessionModel, out)
	})

	t.Run("nested config wins where set", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := goGitBase(t, c).
			WithEnvVariable("ANTHROPIC_MODEL", "claude-nested-override").
			With(daggerExecRaw("core", "llm", "model")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "claude-nested-override", out)
	})

	t.Run("credentials stay with the main client", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		// Inheritance must not hand out the keys: resolving the credential
		// env var directly still happens inside the nested container, which
		// doesn't have it.
		_, err := goGitBase(t, c).
			With(daggerExecRaw("core", "secret", "--uri", "env://ANTHROPIC_API_KEY", "plaintext")).
			Sync(ctx)
		requireErrOut(t, err, `secret env var not found: "ANT..."`)
	})
}
