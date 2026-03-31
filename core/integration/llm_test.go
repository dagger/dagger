package core

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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
	"github.com/dagger/dagger/core/llmconfig"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"gotest.tools/v3/golden"
)

/* NOTE: To update golden test examples, run e.g.:
dagger call test update --pkg=./core/integration --run="TestLLM" --env-file=file://$PWD/.env -o .
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

var (
	// llm-test-module passes a prompt to LLM and sets a random string variable to bust cache
	directCallModuleRef = "github.com/dagger/dagger-test-modules/llm-dir-module-depender/llm-test-module"
	// llm-dir-module-depender depends on directCall module via a relative path
	dependerModuleRef = "github.com/dagger/dagger-test-modules/llm-dir-module-depender"
)

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
				out, err := ctr.
					With(daggerForwardSecrets(c)).
					With(daggerCall(append([]string{"save"}, flags...)...)).
					Stdout(ctx)
				require.NoError(t, err)

				if dir := filepath.Dir(recording); dir != "." {
					err := os.MkdirAll(dir, 0755)
					require.NoError(t, err)
				}
				err = os.WriteFile(recording, []byte(out), 0644)
				require.NoError(t, err)
			}

			replayData, err := os.ReadFile(recording)
			require.NoError(t, err)
			model := "replay/" + base64.StdEncoding.EncodeToString(replayData)

			t.Run("call", func(ctx context.Context, t *testctx.T) {
				cmd := []string{"--model=" + model, "run"}
				cmd = append(cmd, flags...)
				cmd = append(cmd, "file", "--path=main.go", "contents")
				out, err := ctr.With(daggerCall(cmd...)).Stdout(ctx)
				require.NoError(t, err)
				testGoProgram(ctx, t, c, dag.Directory().WithNewFile("main.go", out).File("main.go"), regexp.MustCompile("(?i)hello(.*)world"))
			})

			t.Run("shell", func(ctx context.Context, t *testctx.T) {
				var flags []string
				for _, flag := range tc.Flags {
					flags = append(flags, flag.ToShell()...)
				}
				out, err := ctr.
					With(daggerShell(fmt.Sprintf(`. --model="%s" | run %s | file main.go | contents`, model, strings.Join(flags, " ")))).
					Stdout(ctx)
				require.NoError(t, err)
				testGoProgram(ctx, t, c, dag.Directory().WithNewFile("main.go", out).File("main.go"), regexp.MustCompile("(?i)hello(.*)world"))
			})
		})
	}
}

func (LLMSuite) TestAPILimit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctrFn := func(llmFlags string) dagger.WithContainerFunc {
		return daggerShell(fmt.Sprintf(`llm %s | with-env $(env | with-container-input "alpine" alpine "an alpine linux container") | with-prompt "tell me the value of PATH" | loop | with-prompt "now tell me the value of TERM" | loop --max-api-calls=1 | historyJSON`, llmFlags))
	}

	recording := "llmtest/api-limit.golden"
	if golden.FlagUpdate() {
		out, err := daggerCliBase(t, c).
			With(daggerForwardSecrets(c)).
			With(ctrFn("")).
			Stdout(ctx)
		require.NoError(t, err)

		if dir := filepath.Dir(recording); dir != "." {
			err := os.MkdirAll(dir, 0755)
			require.NoError(t, err)
		}
		err = os.WriteFile(recording, []byte(out), 0644)
		require.NoError(t, err)
	}

	replayData, err := os.ReadFile(recording)
	require.NoError(t, err)
	llmFlags := fmt.Sprintf("--model=\"replay/%s\"", base64.StdEncoding.EncodeToString(replayData))

	_, err = daggerCliBase(t, c).
		With(ctrFn(llmFlags)).
		Stdout(ctx)
	requireErrOut(t, err, "reached API call limit: 1")
}

func (LLMSuite) TestAllowLLM(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	recording := "llmtest/allow-llm.golden"
	if golden.FlagUpdate() {
		out, err := daggerCliBase(t, c).
			With(daggerForwardSecrets(c)).
			// shared recording amongst subtests, they all do basically the same thing
			With(daggerCall("-m", directCallModuleRef, "--allow-llm=all", "save", "--string-arg", "greet me")).
			Stdout(ctx)
		require.NoError(t, err)
		if dir := filepath.Dir(recording); dir != "." {
			err := os.MkdirAll(dir, 0755)
			require.NoError(t, err)
		}
		err = os.WriteFile(recording, []byte(out), 0644)
		require.NoError(t, err)
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
				module:   directCallModuleRef,
				allowLLM: "all",
			},
			{
				name:     "direct allow specific module",
				module:   directCallModuleRef,
				allowLLM: directCallModuleRef,
			},
			{
				name:     "depender allow all",
				module:   dependerModuleRef,
				allowLLM: "all",
			},
			{
				name:     "depender allow specific module",
				module:   dependerModuleRef,
				allowLLM: directCallModuleRef,
			},
			// we only test various permutations of remote module LLM use, local modules don't require the flag and that's covered by the toy-programmer case
		}

		for _, tc := range tcs {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				args := []string{"--allow-llm", tc.allowLLM, modelFlag, "save", "--string-arg", "greet me"}

				_, err := daggerCliBase(t, c).
					With(daggerCallAt(tc.module, args...)).
					Stdout(ctx)
				require.NoError(t, err)
			})
		}
	})

	t.Run("noninteractive prompt fail", func(ctx context.Context, t *testctx.T) {
		args := []string{modelFlag, "save", "--string-arg", t.Name()}

		_, err := daggerCliBase(t, c).
			With(daggerCallAt(directCallModuleRef, args...)).
			Stdout(ctx)
		require.Error(t, err)
	})

	t.Run("environment variable", func(ctx context.Context, t *testctx.T) {
		_, err := daggerCliBase(t, c).
			WithEnvVariable("DAGGER_ALLOW_LLM", "all").
			With(daggerCallAt(dependerModuleRef, modelFlag, "save", "--string-arg", "greet me")).
			Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("shell allow all", func(ctx context.Context, t *testctx.T) {
		_, err := daggerCliBase(t, c).
			WithExec([]string{"dagger", "-m", dependerModuleRef, "--allow-llm=all"}, dagger.ContainerWithExecOpts{
				Stdin:                         fmt.Sprintf(`. %s | save "greet me"`, modelFlag),
				ExperimentalPrivilegedNesting: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("shell interactive module loads", func(ctx context.Context, t *testctx.T) {
		_, err := daggerCliBase(t, c).
			WithExec([]string{"dagger", "--allow-llm", directCallModuleRef}, dagger.ContainerWithExecOpts{
				Stdin:                         fmt.Sprintf(`%s %s | save "greet me"`, dependerModuleRef, modelFlag),
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
				module:   directCallModuleRef,
			},
			// TODO: find a way to test plain tui.
			// under test, it doesn't acknowledge input, but works fine irl
			// {
			// 	name:     "plain tui direct remote module call",
			// 	allowLLM: "",
			// 	module:   directCallModuleRef,
			// 	plain:    true,
			// },
			{
				name:     "allowed unrelated, calling direct",
				allowLLM: "github.com/dagger/dagger",
				module:   directCallModuleRef,
			},
			{
				name:     "allowed depender, calling direct",
				allowLLM: dependerModuleRef,
				module:   directCallModuleRef,
			},
			{
				// this should prompt for the dependency
				name:     "allowed depender, calling depender",
				allowLLM: dependerModuleRef,
				module:   dependerModuleRef,
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
					progressFlag, "call", "-m", tc.module, "--allow-llm", tc.allowLLM, modelFlag, "save", "--string-arg", fmt.Sprintf("greet me %d", i),
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

// TestWorkspaceStage verifies that an LLM tool that returns a Changeset
// correctly stages changes in the host workspace. This exercises the full
// path: LLM → module tool call → Changeset return → Workspace.stage.
func (LLMSuite) TestWorkspaceStage(ctx context.Context, t *testctx.T) {
	configPath := llmconfig.ConfigFile
	if !llmconfig.LLMConfigured() {
		t.Skip("no LLM config found; pass --config-file to engine-dev test")
	}
	c := connect(ctx, t, dagger.WithConfigPath(configPath))

	// The Dang module provides both:
	//  - A `write` tool that creates a file and returns Changeset
	//  - A `run` entrypoint that sets up an LLM session with itself as tools
	base := workspaceBase(t, c).
		WithNewFile("existing.txt", "original content").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "init"}).
		With(initDangModule("stage-test", `
type StageTest {
  """
  Run an LLM session that uses the write tool.
  """
  pub run(source: Workspace!): LLM! {
    llm
      .withEnv(env.withCurrentModule.withWorkspace(source))
      .withPrompt("Use the StageTest write tool to create a file called 'hello.txt' with the content 'hello world'. Do not use any other tool.")
      .loop
  }

  """
  Create a file in the workspace and stage the changes.
  """
  pub write(
    source: Workspace!,
    """
    Path of the file to create.
    """
    path: String!,
    """
    Content to write.
    """
    content: String!,
  ): Void {
    let base = source.directory(".", exclude: ["*"])
    source.stage(changes: base.withNewFile(path, contents: content).changes(base))
    null
  }
}
`)).
		// Mount the LLM config so the inner dagger call can route LLM requests.
		// The file is read from the test process's filesystem (propagated via
		// --config-file flag from the host).
		WithMountedSecret("/root/.config/dagger/config.toml",
			c.Secret("file://"+configPath))

	result := base.With(daggerCall("stage-test", "run", "last-reply"))

	// The LLM should have completed and returned a reply.
	reply, err := result.Stdout(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(reply), "LLM should have replied")

	// The file should have been staged in git.
	statusOut, err := result.
		WithExec([]string{"git", "status", "--porcelain"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, statusOut, "hello.txt",
		"hello.txt should appear in git status after staging")

	// The file should exist on disk with the expected content.
	fileOut, err := result.
		WithExec([]string{"cat", "hello.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello world", fileOut,
		"hello.txt should have the expected content")
}

// TestWorkspaceStageMultipleEdits verifies that staging multiple sequential
// edits to the same file preserves all changes in the git index. This
// reproduces a bug where the second edit would overwrite the first edit's
// staging — the working tree had both edits, but only the second was staged.
func (LLMSuite) TestWorkspaceStageMultipleEdits(ctx context.Context, t *testctx.T) {
	configPath := llmconfig.ConfigFile
	if !llmconfig.LLMConfigured() {
		t.Skip("no LLM config found; pass --config-file to engine-dev test")
	}
	c := connect(ctx, t, dagger.WithConfigPath(configPath))

	base := workspaceBase(t, c).
		WithNewFile("target.txt", "line 1\nline 2\nline 3\n").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "init"}).
		With(initDangModule("multi-edit-test", `
type MultiEditTest {
  """
  Run an LLM session that edits a file twice: once at the start, once at the end.
  """
  pub run(source: Workspace!): LLM! {
    llm
      .withEnv(env.withCurrentModule.withWorkspace(source))
      .withPrompt(
        "Edit the file target.txt twice using the MultiEditTest edit tool:\n" +
        "1. First, replace 'line 1' with 'HEADER\nline 1'\n" +
        "2. Then, replace 'line 3' with 'line 3\nFOOTER'\n" +
        "Make exactly two separate edit tool calls. Do not use any other tool.",
      )
      .loop
  }

  """
  Edit a file by replacing exact text.
  """
  pub edit(
    source: Workspace!,
    """
    Relative path within the workspace
    """
    filePath: String!,
    """
    Exact text to find
    """
    oldText: String!,
    """
    Replacement text
    """
    newText: String!,
  ): Changeset! {
    let normalizedPath = filePath
    let base = source.directory(".", include: [normalizedPath])
    let changes = base
      .withFile(
        normalizedPath,
        source
          .file(normalizedPath)
          .withReplaced(oldText, newText),
      )
      .changes(base)
    source.stage(changes)
    changes
  }
}
`)).
		WithMountedSecret("/root/.config/dagger/config.toml",
			c.Secret("file://"+configPath))

	result := base.With(daggerCall("multi-edit-test", "run", "last-reply"))

	// The LLM should have completed and returned a reply.
	reply, err := result.Stdout(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(reply), "LLM should have replied")

	// The file should have both edits on disk.
	fileOut, err := result.
		WithExec([]string{"cat", "target.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, fileOut, "HEADER", "working tree should contain the first edit")
	require.Contains(t, fileOut, "FOOTER", "working tree should contain the second edit")

	// Both edits should be staged (in the index). Use "git diff --cached"
	// to see what's staged relative to HEAD.
	diffOut, err := result.
		WithExec([]string{"git", "diff", "--cached", "target.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, diffOut, "HEADER",
		"first edit (HEADER) should be staged in the index")
	require.Contains(t, diffOut, "FOOTER",
		"second edit (FOOTER) should be staged in the index")

	// There should be no unstaged changes — working tree should match the index.
	unstagedDiff, err := result.
		WithExec([]string{"git", "diff", "target.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Empty(t, strings.TrimSpace(unstagedDiff),
		"there should be no unstaged changes — both edits should be fully staged")
}

// TestWorkspaceCommit verifies that an LLM tool can create a file, stage the
// changeset, and commit it to the current branch. The module's run method
// orchestrates the LLM, whose write tool returns a Changeset. The LLM loop
// auto-stages the changeset, then the module commits the result.
func (LLMSuite) TestWorkspaceCommit(ctx context.Context, t *testctx.T) {
	configPath := llmconfig.ConfigFile
	if !llmconfig.LLMConfigured() {
		t.Skip("no LLM config found; pass --config-file to engine-dev test")
	}
	c := connect(ctx, t, dagger.WithConfigPath(configPath))

	base := workspaceBase(t, c).
		WithNewFile("existing.txt", "original content").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "init"}).
		With(initDangModule("commit-test", `
type CommitTest {
  """
  Run an LLM session that creates and commits a file via the write tool.
  """
  pub run(source: Workspace!): LLM! {
    llm
      .withEnv(env.withCurrentModule.withWorkspace(source))
      .withPrompt("Use the CommitTest write tool to create a file called 'hello.txt' with the content 'hello world'. Do not use any other tool.")
      .loop
  }

  """
  Create a file in the workspace, stage it, and commit.
  Returns the commit hash.
  """
  pub write(
    source: Workspace!,
    """
    Path of the file to create.
    """
    path: String!,
    """
    Content to write.
    """
    content: String!,
  ): String! {
    let base = source.directory(".", exclude: ["*"])
    source.stage(changes: base.withNewFile(path, contents: content).changes(base))
    source.commit(message: "feat: add hello")
  }
}
`)).
		WithMountedSecret("/root/.config/dagger/config.toml",
			c.Secret("file://"+configPath))

	result := base.With(daggerCall("commit-test", "run", "last-reply"))

	// The LLM should have completed and returned a reply.
	reply, err := result.Stdout(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(reply), "LLM should have replied")

	// Verify the commit message on the current branch.
	logOut, err := result.
		WithExec([]string{"git", "log", "--oneline", "-1"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, logOut, "feat: add hello")

	// Verify the file exists on disk with expected content.
	fileOut, err := result.
		WithExec([]string{"cat", "hello.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello world", fileOut)
}

// TestWorkspaceCommitWithBranch verifies that an LLM tool can create a file,
// stage the changeset, and commit it to a separate worktree branch — leaving
// the main branch untouched.
func (LLMSuite) TestWorkspaceCommitWithBranch(ctx context.Context, t *testctx.T) {
	configPath := llmconfig.ConfigFile
	if !llmconfig.LLMConfigured() {
		t.Skip("no LLM config found; pass --config-file to engine-dev test")
	}
	c := connect(ctx, t, dagger.WithConfigPath(configPath))

	base := workspaceBase(t, c).
		WithNewFile("existing.txt", "original content").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "init"}).
		With(initDangModule("branch-test", `
type BranchTest {
  """
  Run an LLM session that creates and commits a file on a feature branch.
  """
  pub run(source: Workspace!): LLM! {
    let ws = source.withBranch("agent/work")
    llm
      .withEnv(env.withCurrentModule.withWorkspace(ws))
      .withPrompt("Use the BranchTest write tool to create a file called 'hello.txt' with the content 'hello world'. Do not use any other tool.")
      .loop
  }

  """
  Create a file in the workspace, stage it, and commit.
  Returns the commit hash.
  """
  pub write(
    source: Workspace!,
    """
    Path of the file to create.
    """
    path: String!,
    """
    Content to write.
    """
    content: String!,
  ): String! {
    let base = source.directory(".", exclude: ["*"])
    source.stage(changes: base.withNewFile(path, contents: content).changes(base))
    source.commit(message: "feat: add hello")
  }
}
`)).
		WithMountedSecret("/root/.config/dagger/config.toml",
			c.Secret("file://"+configPath))

	result := base.With(daggerCall("branch-test", "run", "last-reply"))

	// The LLM should have completed and returned a reply.
	reply, err := result.Stdout(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(reply), "LLM should have replied")

	// Verify the commit message in the worktree branch.
	logOut, err := result.
		WithWorkdir("/work-worktrees/agent-work").
		WithExec([]string{"git", "log", "--oneline", "-1"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, logOut, "feat: add hello")

	// Verify the file exists in the worktree with expected content.
	fileOut, err := result.
		WithWorkdir("/work-worktrees/agent-work").
		WithExec([]string{"cat", "hello.txt"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello world", fileOut)

	// The main branch should be untouched.
	mainLog, err := result.
		WithExec([]string{"git", "log", "--oneline", "-1"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, mainLog, "init",
		"main branch should still point at the init commit")
}

// TestToolResponse verifies that the LLM can see the return value of a tool
// call. A tool returns a known string, and we ask the LLM to repeat it back.
// This catches bugs where tool responses are sent as empty strings.
func (LLMSuite) TestToolResponse(ctx context.Context, t *testctx.T) {
	configPath := llmconfig.ConfigFile
	if !llmconfig.LLMConfigured() {
		t.Skip("no LLM config found; pass --config-file to engine-dev test")
	}
	c := connect(ctx, t, dagger.WithConfigPath(configPath))

	base := workspaceBase(t, c).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "init"}).
		With(initDangModule("echo-test", `
type EchoTest {
  """
  Run an LLM session that calls the secret-code tool and reports the result.
  """
  pub run(source: Workspace!): LLM! {
    llm
      .withEnv(env.withCurrentModule.withWorkspace(source))
      .withPrompt("Call the EchoTest secretCode tool, then reply with ONLY the exact string it returned, nothing else.")
      .loop
  }

  """
  Returns a secret code word.
  """
  pub secretCode(): String! {
    "flamingo-42"
  }
}
`)).
		WithMountedSecret("/root/.config/dagger/config.toml",
			c.Secret("file://"+configPath))

	result := base.With(daggerCall("echo-test", "run", "last-reply"))

	reply, err := result.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(reply), "flamingo-42",
		"LLM should have seen and repeated the tool's return value")
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
		return ctr.WithMountedSecret(".env", dag.Secret("file:///dagger.env"))
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

// TestAutoConstructBuilderPattern verifies that when a module tool returns
// the same type as the auto-constructed type (builder pattern), subsequent
// calls chain on the returned object instead of re-constructing from scratch.
// For example, calling withName("Alice") then withGreeting("Hi") should
// produce an object with both values, not just the last one.
func (LLMSuite) TestAutoConstructBuilderPattern(ctx context.Context, t *testctx.T) {
	configPath := llmconfig.ConfigFile
	if !llmconfig.LLMConfigured() {
		t.Skip("no LLM config found; pass --config-file to engine-dev test")
	}
	c := connect(ctx, t, dagger.WithConfigPath(configPath))

	base := workspaceBase(t, c).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "init"}).
		With(initDangModule("builder-test", `
type BuilderTest {
  pub name: String! = ""
  pub greeting: String! = ""

  """
  Set the name and return the updated builder.
  """
  pub withName(
    """
    The name to set.
    """
    name: String!,
  ): BuilderTest! {
    self.name = name
    self
  }

  """
  Set the greeting and return the updated builder.
  """
  pub withGreeting(
    """
    The greeting to set.
    """
    greeting: String!,
  ): BuilderTest! {
    self.greeting = greeting
    self
  }

  """
  Describe the current state. Returns a string combining the greeting and name.
  """
  pub describe(): String! {
    greeting + ", " + name + "!"
  }

  """
  Run an LLM session that exercises the builder pattern.
  """
  pub run(source: Workspace!): LLM! {
    llm
      .withEnv(env.withCurrentModule.withWorkspace(source))
      .withPrompt(
        "Do the following in order:\n" +
        "1. Call the BuilderTest withName tool with name 'Alice'\n" +
        "2. Call the BuilderTest withGreeting tool with greeting 'Hello'\n" +
        "3. Call the BuilderTest describe tool\n" +
        "Reply with ONLY the exact string returned by describe, nothing else.",
      )
      .loop
  }
}
`)).
		WithMountedSecret("/root/.config/dagger/config.toml",
			c.Secret("file://"+configPath))

	reply, err := base.With(daggerCall("builder-test", "run", "last-reply")).Stdout(ctx)
	require.NoError(t, err)

	reply = strings.TrimSpace(reply)
	t.Logf("LLM reply: %s", reply)

	// If builder chaining works, both withName and withGreeting accumulate on
	// the same object, and describe() returns "Hello, Alice!".
	// If it's broken (re-constructing each time), describe() would return
	// ", Alice!" or "Hello, !" depending on which call was last.
	require.Contains(t, reply, "Hello, Alice!",
		"describe() should reflect both withName and withGreeting calls chained together")
}

// TestToolCallLogRollup verifies that logs written by engine operations
// (e.g. Workspace.search writing grep-like output to span stdio) appear
// in the LLM's tool call result, not just the tool function's own return
// value. This is the "log roll-up" feature in MCP.captureLogs.
func (LLMSuite) TestToolCallLogRollup(ctx context.Context, t *testctx.T) {
	configPath := llmconfig.ConfigFile
	if !llmconfig.LLMConfigured() {
		t.Skip("no LLM config found; pass --config-file to engine-dev test")
	}
	c := connect(ctx, t, dagger.WithConfigPath(configPath))

	filename := rand.Text()
	base := workspaceBase(t, c).
		WithNewFile(filename+".txt", "line 1\nline 2\nline 3\nHello!\nline 5\n").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "init"}).
		With(initDangModule("grep-test", `
type GrepTest {
  """
  Run an LLM session that searches for a pattern.
  """
  pub run(source: Workspace!): LLM! {
    llm
      .withEnv(env.withCurrentModule.withWorkspace(source))
      .withPrompt("Use the grep tool to search for the literal text 'Hello!' in the workspace. Respond with the exact output. Do not use any other tool.")
      .loop
  }

  """
  Search the workspace for a pattern and return a summary.
  The actual grep-like results are written to span stdio by
  Workspace.search and should appear in the tool call logs.
  """
  pub grep(
    source: Workspace!,
    """
    Pattern to search for.
    """
    pattern: String!,
  ): String! {
    let matches = source.search(pattern: pattern, literal: true, globs: ["*.txt"])
    toJSON(matches.{id}.length) + " result found"
  }
}
`)).
		WithMountedSecret("/root/.config/dagger/config.toml",
			c.Secret("file://"+configPath))

	reply, err := base.With(daggerCall("grep-test", "run", "last-reply")).Stdout(ctx)
	require.NoError(t, err)

	reply = strings.TrimSpace(reply)
	t.Logf("LLM reply: %s", reply)

	// The reply should contain the grep-like output from Workspace.search
	// (rolled up from span stdio), not just the tool's return value.
	require.Contains(t, reply, filename, "reply should contain the filename from search results")
	require.Contains(t, reply, ":4:", "reply should contain the line number")
	require.Contains(t, reply, "Hello!", "reply should contain the matched content")
	require.Contains(t, reply, "1 result found", "reply should contain the tool's summary")
}
