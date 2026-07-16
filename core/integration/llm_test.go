package core

// These tests cover module functions that call Dagger's LLM API. They verify
// direct calls, `dagger shell` argument handling, API limit errors, and the
// `--allow-llm` permission gate.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

// TestWithResetWorkspace verifies that withResetWorkspace re-emits the session
// as a flat, data-only recipe: the conversation survives byte-for-byte, but
// the workspace overlays recorded during the session (withWorkspace nodes with
// withChanges derivations) are dropped, so a persisted globalID no longer
// replays workspace edits when loaded. This is what makes ctrl+s (export +
// reset) durable: replaying an edit chain against already-updated files fails
// with "search string not found" or silently re-applies.
func (LLMSuite) TestWithResetWorkspace(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	llm := c.LLM().
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

	reset := llmEdited.WithResetWorkspace()

	// The conversation is preserved exactly.
	origHist, err := llmEdited.Transcript(ctx)
	require.NoError(t, err)
	resetHist, err := reset.Transcript(ctx)
	require.NoError(t, err)
	require.Equal(t, origHist, resetHist)

	// The persisted recipe is flat: no withResetWorkspace node, and no
	// workspace rebind carrying overlay derivations on the spine.
	globalID, err := reset.PortableID(ctx)
	require.NoError(t, err)
	gid := new(call.ID)
	require.NoError(t, gid.Decode(string(globalID)))
	for cur := gid; cur != nil; cur = cur.Receiver() {
		require.NotEqual(t, "withResetWorkspace", cur.Field(),
			"reset must re-emit the recipe, not append to it")
		require.NotEqual(t, "withWorkspace", cur.Field(),
			"a currentWorkspace-based binding must be dropped from the recipe "+
				"so replay re-resolves the live workspace")
	}

	// The reset session reloads with the conversation intact.
	reloaded := dagger.Ref[*dagger.LLM](c, globalID)
	reply, err := reloaded.LastReply(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello world", reply)
	reloadedHist, err := reloaded.Transcript(ctx)
	require.NoError(t, err)
	require.Equal(t, origHist, reloadedHist)
}
