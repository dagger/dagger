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
	"gotest.tools/v3/golden"
)

/* NOTE: To update golden test examples, run the tests on the host against a
dev engine (so -update writes the goldens back into the worktree), with an env
file containing live provider credentials:

	env DAGGER_LLM_TEST_ENV=$PWD/.env ./hack/with-dev go test ./core/integration/ -run TestLLM -count=1 -update

(engine-dev test --update runs -update inside the test container and discards
the recorded goldens.)
*/

type LLMSuite struct{}

func TestLLM(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(LLMSuite{})
}

type LLMTestCase struct {
	Ref   string
	Name  string
	Flags []LLMTestCaseFlag
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

// recordMessages runs a raw GraphQL query that drives a conversation against
// live providers and renders the messages export at the given gjson path as a
// replay recording. The query should select messages{llmMessagesSelection}.
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

func writeRecording(t *testctx.T, path string, data []byte) {
	t.Helper()
	if dir := filepath.Dir(path); dir != "." {
		require.NoError(t, os.MkdirAll(dir, 0755))
	}
	require.NoError(t, os.WriteFile(path, data, 0644))
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

			recording := fmt.Sprintf("llmtest/%s.golden", tc.Name)
			if golden.FlagUpdate() {
				var args []string
				for _, flag := range tc.Flags {
					args = append(args, fmt.Sprintf("%s: %q", flag.Key, flag.Value))
				}
				out, err := ctr.
					With(daggerForwardSecrets(c)).
					With(daggerQueryAt(".", `{agent(%s){messages{%s}}}`,
						strings.Join(args, ", "), llmMessagesSelection)).
					Stdout(ctx)
				require.NoError(t, err)

				writeRecording(t, recording, messagesGolden(t, out, "agent.messages"))
			}

			replayData, err := os.ReadFile(recording)
			require.NoError(t, err)
			model := "replay/" + base64.StdEncoding.EncodeToString(replayData)

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

	recording := "llmtest/api-limit.golden"
	if golden.FlagUpdate() {
		// Drive the same conversation as the shell pipeline below, minus the
		// step limit, and export its messages as the recording.
		ctrRes, err := testutil.QueryWithClient[struct {
			Container struct {
				From struct {
					ID string
				}
			}
		}](c, t, `{container{from(address:"alpine"){id}}}`, nil)
		require.NoError(t, err)

		out := recordMessages(t, c,
			fmt.Sprintf(`query($ctr: ID!){llm{withTools(object:$ctr){withPrompt(prompt:"tell me the value of PATH"){loop{withPrompt(prompt:"now tell me the value of TERM"){messages{%s}}}}}}}`, llmMessagesSelection),
			map[string]any{"ctr": ctrRes.Container.From.ID},
			"llm.withTools.withPrompt.loop.withPrompt.messages")
		writeRecording(t, recording, out)
	}

	replayData, err := os.ReadFile(recording)
	require.NoError(t, err)
	llmFlags := fmt.Sprintf("--model=\"replay/%s\"", base64.StdEncoding.EncodeToString(replayData))

	_, err = daggerCliBase(t, c).
		With(ctrFn(llmFlags, "--max-steps=1")).
		Stdout(ctx)
	requireErrOut(t, err, "reached step limit: 1")
}

func (LLMSuite) TestAllowLLM(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	recording := "llmtest/allow-llm.golden"
	if golden.FlagUpdate() {
		// shared recording amongst subtests, they all drive the same conversation
		out, err := daggerCliBase(t, c).
			With(daggerForwardSecrets(c)).
			WithExec([]string{"dagger", "query", "-m", directModuleRef, "--allow-llm=all"}, dagger.ContainerWithExecOpts{
				Stdin: fmt.Sprintf(`{agent(stringArg:"greet me", cacheBuster:%q){messages{%s}}}`,
					identity.NewID(), llmMessagesSelection),
				ExperimentalPrivilegedNesting: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		writeRecording(t, recording, messagesGolden(t, out, "agent.messages"))
	}

	replayData, err := os.ReadFile(recording)
	require.NoError(t, err)
	modelFlag := fmt.Sprintf("--model=replay/%s", base64.StdEncoding.EncodeToString(replayData))

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

func daggerForwardSecrets(dag *dagger.Client) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		envPath := os.Getenv("DAGGER_LLM_TEST_ENV")
		if envPath == "" {
			envPath = "/dagger.env"
		}
		return ctr.WithMountedSecret(".env", dag.Secret("file://"+envPath))
	}

	// 	return func(ctr *dagger.Container) *dagger.Container {
	// 		propagate := func(env string) {
	// 			if v, ok := os.LookupEnv(env); ok {
	// 				ctr = ctr.WithSecretVariable(env, dag.SetSecret(env, v))
	// 			}
	// 		}

	// 		propagate("ANTHROPIC_API_KEY")
	// 		propagate("ANTHROPIC_BASE_URL")
	// 		propagate("ANTHROPIC_MODEL")

	// 		propagate("OPENAI_API_KEY")
	// 		propagate("OPENAI_AZURE_VERSION")
	// 		propagate("OPENAI_BASE_URL")
	// 		propagate("OPENAI_MODEL")

	// 		propagate("GEMINI_API_KEY")
	// 		propagate("GEMINI_BASE_URL")
	// 		propagate("GEMINI_MODEL")

	//		return ctr
	//	}
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
