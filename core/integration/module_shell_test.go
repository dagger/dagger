package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/Netflix/go-expect"
	"github.com/creack/pty"
	"github.com/stretchr/testify/require"
)

// Shells tests are run directly on the host rather than in exec containers because we want to
// directly interact with the dagger shell tui without resorting to embedding more go code
// into a container for driving it.

func TestModuleDaggerShell(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t.Run("basic", func(t *testing.T) {
		modDir := t.TempDir()
		err := os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main
import "context"

func New(ctx context.Context) *Test {
	return &Test{
		Ctr: dag.Container().
			From("mirror.gcr.io/alpine:3.18").
			WithEnvVariable("COOLENV", "woo").
			WithWorkdir("/coolworkdir").
			WithDefaultShell([]string{"/bin/sh"}),
	}
}

type Test struct {
	Ctr *Container
}
`), 0644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "--debug", "mod", "init", "--name=test", "--sdk=go")
		require.NoError(t, err)

		// cache the module load itself so there's less to wait for in the shell invocation below
		_, err = hostDaggerExec(ctx, t, modDir, "--debug", "functions")
		require.NoError(t, err)

		// timeout for waiting for each expected line is very generous in case CI is under heavy load or something
		console, err := newShellTestConsole(60 * time.Second)
		require.NoError(t, err)
		defer console.Close()

		tty := console.Tty()

		// We want the size to be big enough to fit the output we're expecting, but increasing
		// the size also eventually slows down the tests due to more output being generated and
		// needing parsing.
		err = pty.Setsize(tty, &pty.Winsize{Rows: 6, Cols: 16})
		require.NoError(t, err)

		cmd := hostDaggerCommand(ctx, t, modDir, "call", "ctr", "shell")
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		err = cmd.Start()
		require.NoError(t, err)

		err = console.ExpectLineRegex(ctx, "/coolworkdir #")
		require.NoError(t, err)

		_, err = console.SendLine("echo $COOLENV")
		require.NoError(t, err)

		err = console.ExpectLineRegex(ctx, "woo")
		require.NoError(t, err)

		err = console.ExpectLineRegex(ctx, "/coolworkdir #")
		require.NoError(t, err)

		_, err = console.SendLine("exit")
		require.NoError(t, err)

		go console.ExpectEOF()

		err = cmd.Wait()
		require.NoError(t, err)
	})

	t.Run("override args", func(t *testing.T) {
		modDir := t.TempDir()
		err := os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main
import "context"

func New(ctx context.Context) *Test {
	return &Test{
		Ctr: dag.Container().
			From("mirror.gcr.io/alpine:3.18").
			WithEnvVariable("COOLENV", "woo").
			WithWorkdir("/coolworkdir").
			WithExec([]string{"apk", "add", "python3"}).
			WithDefaultShell([]string{"/bin/sh"}),
	}
}

type Test struct {
	Ctr *Container
}
`), 0644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "--debug", "mod", "init", "--name=test", "--sdk=go")
		require.NoError(t, err)

		// cache the returned container so there's less to wait for in the shell invocation below
		_, err = hostDaggerExec(ctx, t, modDir, "--debug", "call", "ctr", "sync")
		require.NoError(t, err)

		console, err := newShellTestConsole(60 * time.Second)
		require.NoError(t, err)
		defer console.Close()

		tty := console.Tty()

		err = pty.Setsize(tty, &pty.Winsize{Rows: 5, Cols: 22})
		require.NoError(t, err)

		cmd := hostDaggerCommand(ctx, t, modDir, "call", "ctr", "shell", "--args=python")
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		err = cmd.Start()
		require.NoError(t, err)

		err = console.ExpectLineRegex(ctx, ">>> ")
		require.NoError(t, err)

		_, err = console.SendLine("import os")
		require.NoError(t, err)

		err = console.ExpectLineRegex(ctx, ">>> ")
		require.NoError(t, err)

		_, err = console.SendLine("os.environ['COOLENV']")
		require.NoError(t, err)

		err = console.ExpectLineRegex(ctx, "'woo'")
		require.NoError(t, err)

		_, err = console.SendLine("exit()")
		require.NoError(t, err)

		go console.ExpectEOF()

		err = cmd.Wait()
		require.NoError(t, err)
	})
}

// shellTestConsole wraps expect.Console with methods that allow us to enforce timeouts despite
// the fact that the TUI is constantly writing more data (which invalidates the expect lib's builtin
// read timeout mechanisms).
type shellTestConsole struct {
	*expect.Console
	expectLineTimeout time.Duration
	output            *bytes.Buffer
}

func newShellTestConsole(expectLineTimeout time.Duration) (*shellTestConsole, error) {
	output := bytes.NewBuffer(nil)
	console, err := expect.NewConsole(expect.WithStdout(output), expect.WithDefaultTimeout(expectLineTimeout))
	if err != nil {
		return nil, err
	}
	return &shellTestConsole{
		Console:           console,
		expectLineTimeout: expectLineTimeout,
		output:            output,
	}, nil
}

func (e *shellTestConsole) ExpectLineRegex(ctx context.Context, pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, e.expectLineTimeout)
	defer cancel()
	lineMatcher := expect.RegexpPattern(".*\n")
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for line matching %q, most recent output:\n%s", pattern, e.output.String())
		default:
		}

		line, err := e.Expect(lineMatcher)
		if err != nil {
			return err
		}
		if re.MatchString(line) {
			return nil
		}
	}
}
