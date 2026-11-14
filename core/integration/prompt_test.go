package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// PromptSuite covers Query.prompt: a module asking the user a question
// through the engine's prompt broker (Server.Prompt in
// engine/server/prompts.go). The asker module is a Dang module whose
// functions ask for each kind of answer.
type PromptSuite struct{}

func TestPrompt(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(PromptSuite{})
}

// promptSession runs the CLI on the host under a PTY with the plain
// frontend, which asks on the terminal, in a checkout of the prompts
// workspace, with its own configuration directory for remembered answers.
type promptSession struct {
	t       *testctx.T
	console *tuiConsole
	done    chan error
	stop    func() bool
}

func promptWorkdir(ctx context.Context, t *testctx.T) string {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.CopyFS(workdir, os.DirFS(filepath.Join("testdata", "workspaces", "workspace-prompts"))))
	return workdir
}

func startPromptSession(ctx context.Context, t *testctx.T, workdir, configHome, script string) *promptSession {
	t.Helper()
	console, err := newTUIConsole(t, 60*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { console.Close() })
	tty := console.Tty()
	require.NoError(t, pty.Setsize(tty, &pty.Winsize{Rows: 20, Cols: 200}))
	cmd := hostDaggerCommand(ctx, t, workdir, "--progress=plain", "-c", script)
	cmd.Env = append(cmd.Env, "XDG_CONFIG_HOME="+configHome)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	require.NoError(t, cmd.Start())
	// The expect timeouts only bound silence, and the CLI keeps printing while
	// a prompt it never shows waits: bound the whole exchange, so a missed or
	// unanswered prompt fails rather than hangs.
	exchange, cancel := context.WithTimeout(ctx, 3*time.Minute)
	t.Cleanup(cancel)
	stop := context.AfterFunc(exchange, func() {
		_ = cmd.Process.Kill()
		console.Close()
	})
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	return &promptSession{t: t, console: console, done: done, stop: stop}
}

func (s *promptSession) expect(text string) {
	s.t.Helper()
	_, err := s.console.ExpectString(text)
	require.NoError(s.t, err, "waiting for %q", text)
}

func (s *promptSession) answer(line string) {
	s.t.Helper()
	_, err := s.console.SendLine(line)
	require.NoError(s.t, err)
}

// wait waits for the CLI to exit and returns everything it printed.
func (s *promptSession) wait() (string, error) {
	s.t.Helper()
	// Drain the rest of the output (one reader at a time: only now).
	go s.console.ExpectEOF()
	err := <-s.done
	s.stop()
	return s.console.output.String(), err
}

// finish waits for want in the output, then for the CLI to exit
// successfully, and returns everything it printed.
func (s *promptSession) finish(want string) string {
	s.t.Helper()
	s.expect(want)
	out, err := s.wait()
	require.NoError(s.t, err, out)
	return out
}

func (PromptSuite) TestAnswers(ctx context.Context, t *testctx.T) {
	workdir := promptWorkdir(ctx, t)
	configHome := t.TempDir()

	t.Run("boolean", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []struct{ answer, want string }{{"y", "answered yes"}, {"n", "answered no"}} {
			s := startPromptSession(ctx, t, workdir, configHome, `asker | yesno --message="Ship it?"`)
			// The plain frontend asks on the terminal, naming who asks as the
			// engine resolved it.
			s.expect(`Ship it?`)
			s.expect(`(requested by module "asker", function "Asker.yesno")`)
			s.answer(tc.answer)
			s.finish(tc.want)
		}
	})

	t.Run("string", func(ctx context.Context, t *testctx.T) {
		s := startPromptSession(ctx, t, workdir, configHome, `asker | text --message="Your name?"`)
		s.expect(`Your name?`)
		s.answer("Ada Lovelace")
		s.finish("[Ada Lovelace]")
	})

	t.Run("secret", func(ctx context.Context, t *testctx.T) {
		const token = "hunter2-token"
		s := startPromptSession(ctx, t, workdir, configHome, fmt.Sprintf(`asker | token --message="Your token?" --want=%q`, token))
		s.expect(`Your token?`)
		// A masked form (raw mode): enter is a carriage return.
		_, err := s.console.Send(token + "\r")
		require.NoError(t, err)
		s.finish("token matches")
	})
}

func (PromptSuite) TestSessionMemory(ctx context.Context, t *testctx.T) {
	workdir := promptWorkdir(ctx, t)
	configHome := t.TempDir()

	// The same module asking the same question twice in a session is
	// answered once.
	s := startPromptSession(ctx, t, workdir, configHome, `asker | twice --message="Twice?"`)
	s.expect(`Twice?`)
	s.answer("y")
	out := s.finish("yes twice")
	require.Equal(t, 1, strings.Count(out, `(requested by module "asker", function "Asker.twice")`), "asked once:\n%s", out)

	// A function that asked is never reused: another session asks again,
	// and gets its own answer.
	s = startPromptSession(ctx, t, workdir, configHome, `asker | twice --message="Twice?"`)
	s.expect(`Twice?`)
	s.answer("n")
	s.finish("no twice")

	// A function returning an object built from the answer declares itself
	// cached per session: calls on what it returns are not reused either.
	for _, tc := range []struct{ answer, want string }{{"y", "yes"}, {"n", "no"}} {
		s = startPromptSession(ctx, t, workdir, configHome, `asker | build --message="Build?" | file answer | contents`)
		s.expect(`Build?`)
		s.expect(`(requested by module "asker", function "Asker.build")`)
		s.answer(tc.answer)
		s.finish("\n" + tc.want)
	}
}

func (PromptSuite) TestPersistentKey(ctx context.Context, t *testctx.T) {
	workdir := promptWorkdir(ctx, t)
	configHome := t.TempDir()

	// A local module is only a path, which another module could be loaded
	// from: its keyed answers are remembered for the session only, so the
	// next session asks again, and nothing is written for it.
	for _, name := range []string{"Ada", "Grace"} {
		s := startPromptSession(ctx, t, workdir, configHome, `asker | text --message="Your name?" --key=name`)
		s.expect(`Your name?`)
		s.answer(name)
		s.finish("[" + name + "]")
	}
	data, err := os.ReadFile(filepath.Join(configHome, "dagger", "prompt-answers.json"))
	if err == nil {
		require.NotContains(t, string(data), "Grace", "a local module's answers are not remembered")
	} else {
		require.True(t, os.IsNotExist(err), "%v", err)
	}
}

func (PromptSuite) TestNotInteractive(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-prompts")

	t.Run("default", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerShell(`asker | fallback --message="Proceed?"`)).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "true")
	})

	t.Run("boolean without a default answers no", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerShell(`asker | yesno --message="Proceed?"`)).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "answered no")
	})

	t.Run("string without a default answers empty", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerShell(`asker | text --message="Your name?"`)).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "[]")
	})

	t.Run("string with a default", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerShell(`asker | text --message="Your name?" --otherwise=Anonymous`)).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "[Anonymous]")
	})

	t.Run("secret", func(ctx context.Context, t *testctx.T) {
		_, err := base.With(daggerShell(`asker | token --message="Token?" --want=x`)).Stdout(ctx)
		requireErrOut(t, err, "prompting for a secret needs an interactive client")
	})
}
