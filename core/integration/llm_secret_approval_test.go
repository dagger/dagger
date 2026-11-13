package core

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"dagger.io/dagger/core"
	"github.com/creack/pty"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestAgentSecretApproval covers a module reading a host secret on an agent's
// behalf (Server.AuthorizeSecret). The keeper module is a Dang module: its
// function clients have no host of their own, so without an agent it can read
// no host secret it was not passed. A worker agent it spawns may read the
// owner's secret once the owner (the CLI) approves; a secret the owner
// configured for the module itself needs no approval.
func (LLMSuite) TestAgentSecretApproval(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	token := "keeper-token-" + rand.Text()
	const tokenURI = "env://KEEPER_TOKEN"
	base := workspaceFixture(t, c, "workspace-agent-secrets").
		WithEnvVariable("KEEPER_TOKEN", token)
	// recording calls tool with args, calls times, then says done.
	recording := func(prompt, tool string, args map[string]string, calls int) string {
		argsJSON, err := json.Marshal(args)
		require.NoError(t, err)
		llm := core.NewQuery(c).LLM().WithPrompt(prompt)
		for i := range calls {
			id := fmt.Sprintf("call_%d", i+1)
			llm = llm.WithResponse([]core.LLMContentBlockInput{
				{Kind: core.LLMContentBlockKindToolCall, CallID: id, ToolName: tool, Arguments: core.JSON(argsJSON)},
			}).WithToolResult(id, "", false)
		}
		return cannedRecordingModel(ctx, t, c, llm.WithResponse([]core.LLMContentBlockInput{
			{Kind: core.LLMContentBlockKindText, Text: "done"},
		}))
	}
	readToken := map[string]string{"uri": tokenURI, "want": token}

	t.Run("a module without an agent cannot read host secrets", func(ctx context.Context, t *testctx.T) {
		_, err := base.With(daggerShell(fmt.Sprintf(
			`keeper | secret-matches --uri=%q --want=%q`, tokenURI, token,
		))).Stdout(ctx)
		requireErrOut(t, err, "SDK client access to host session attachables is denied")
	})

	t.Run("an agent's module needs the owner's approval", func(ctx context.Context, t *testctx.T) {
		// A non-interactive CLI cannot approve.
		const prompt = "does the token match?"
		out, err := base.With(daggerShell(fmt.Sprintf(
			`keeper | delegate --model="%s" --prompt=%q`, recording(prompt, "secretMatches", readToken, 1), prompt,
		))).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `reading secret "env://KEEPER_TOKEN" requires approval: owning client cannot prompt (not interactive)`)
		require.NotContains(t, out, "secret matches")
	})

	t.Run("a secret the owner configured needs no approval", func(ctx context.Context, t *testctx.T) {
		// dagger.toml sets keeper's token to env://KEEPER_TOKEN: the CLI
		// reads it constructing keeper, so its agent uses it unasked.
		const prompt = "does the configured token match?"
		out, err := base.With(daggerShell(fmt.Sprintf(
			`keeper | delegate --model="%s" --prompt=%q`,
			recording(prompt, "tokenMatches", map[string]string{"want": token}, 1), prompt,
		))).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "token matches")
		require.NotContains(t, out, "requires approval")
	})

	t.Run("the owner approves or denies each read", func(ctx context.Context, t *testctx.T) {
		// Answering the prompt takes a terminal, so run the CLI on the host
		// under a PTY. The plain frontend asks a yes/no question on the
		// terminal (the approval fallback, where yes allows for the session);
		// the pretty TUI renders approvals inline, with keys of its own. The
		// worker reads twice: the first read is denied, and the denial must
		// not stick.
		const prompt = "does the token match, approved?"
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)
		require.NoError(t, os.CopyFS(workdir, os.DirFS(filepath.Join("testdata", "workspaces", "workspace-agent-secrets"))))

		console, err := newTUIConsole(t, 60*time.Second)
		require.NoError(t, err)
		defer console.Close()
		tty := console.Tty()
		require.NoError(t, pty.Setsize(tty, &pty.Winsize{Rows: 20, Cols: 200}))
		cmd := hostDaggerCommand(ctx, t, workdir, "--progress=plain", "-c",
			fmt.Sprintf(`keeper | delegate --model="%s" --prompt=%q`, recording(prompt, "secretMatches", readToken, 2), prompt))
		cmd.Env = append(cmd.Env, "KEEPER_TOKEN="+token)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
		require.NoError(t, cmd.Start())
		// The expect timeouts only bound silence, and the CLI keeps printing
		// while a prompt it never shows waits: bound the whole exchange, so a
		// prompt that is missed or never answered fails rather than hangs.
		exchange, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		stop := context.AfterFunc(exchange, func() {
			_ = cmd.Process.Kill()
			console.Close()
		})
		defer stop()
		expectString := func(s string) {
			t.Helper()
			_, err := console.ExpectString(s)
			require.NoError(t, err, "waiting for %q (deadline: %v)", s, exchange.Err())
		}

		// The plain frontend asks on the terminal at once, while it prints
		// spans (the first read's denial among them) as they end: expect the
		// questions in order, and the denial in the output afterwards.
		for _, answer := range []string{"n", "y"} {
			expectString(`Allow reading secret "env://KEEPER_TOKEN" (requested by agent "keeper-worker", module "keeper"`)
			_, err = console.SendLine(answer)
			require.NoError(t, err)
		}
		expectString("secret matches")
		go console.ExpectEOF()
		require.NoError(t, cmd.Wait())
		require.NoError(t, exchange.Err(), "the exchange ran out of time")
		stop()
		require.Contains(t, console.output.String(), `reading secret "env://KEEPER_TOKEN" was denied by the owning client`)

		// The approved call's result is derived from the owner's secret,
		// while its cache key only names its arguments: the same call
		// without an agent must not be answered from it.
		again := hostDaggerCommand(ctx, t, workdir, "-c",
			fmt.Sprintf(`keeper | secret-matches --uri=%q --want=%q`, tokenURI, token))
		again.Env = append(again.Env, "KEEPER_TOKEN="+token)
		out, err := again.CombinedOutput()
		require.Error(t, err, "an approved read's result was reused:\n%s", out)
		require.Contains(t, string(out), "SDK client access to host session attachables is denied")
	})
}
