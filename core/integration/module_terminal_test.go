package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Netflix/go-expect"
	"github.com/containerd/continuity/fs"
	"github.com/creack/pty"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/dagger/testctx"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

// Terminal tests are run directly on the host rather than in exec containers because we want to
// directly interact with the dagger shell tui without resorting to embedding more go code
// into a container for driving it.

const (
	// this is used in some shell prompts
	resetSeq = termenv.CSI + termenv.ResetSeq + "m"
)

func (ModuleSuite) TestDaggerTerminal(ctx context.Context, t *testctx.T) {
	t.Run("default arg /bin/sh", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		err := os.WriteFile(filepath.Join(modDir, "main.go"), []byte(fmt.Sprintf(`package main
import (
	"context"
	"dagger/test/internal/dagger"
)

func New(ctx context.Context) *Test {
	return &Test{
		Ctr: dag.Container().
			From("%s").
			WithEnvVariable("COOLENV", "woo").
			WithWorkdir("/coolworkdir"),
	}
}

type Test struct {
	Ctr *dagger.Container
}
`, alpineImage)), 0644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		// cache the module load itself so there's less to wait for in the shell invocation below
		_, err = hostDaggerExec(ctx, t, modDir, "functions")
		require.NoError(t, err)

		// timeout for waiting for each expected line is very generous in case CI is under heavy load or something
		console, err := newTUIConsole(t, 60*time.Second)
		require.NoError(t, err)
		defer console.Close()

		tty := console.Tty()

		// We want the size to be big enough to fit the output we're expecting, but increasing
		// the size also eventually slows down the tests due to more output being generated and
		// needing parsing.
		err = pty.Setsize(tty, &pty.Winsize{Rows: 6, Cols: 16})
		require.NoError(t, err)

		cmd := hostDaggerCommand(ctx, t, modDir, "call", "ctr", "terminal")
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		err = cmd.Start()
		require.NoError(t, err)

		prompt := fmt.Sprintf("/coolworkdir%s $ ", resetSeq)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("pwd")
		require.NoError(t, err)

		_, err = console.ExpectString("/coolworkdir\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("echo $COOLENV")
		require.NoError(t, err)

		_, err = console.ExpectString("woo\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("exit")
		require.NoError(t, err)

		go console.ExpectEOF()

		err = cmd.Wait()
		require.NoError(t, err)
	})

	t.Run("basic", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		err := os.WriteFile(filepath.Join(modDir, "main.go"), []byte(fmt.Sprintf(`package main

	import (
		"context"
		"dagger/test/internal/dagger"
	)

	func New(ctx context.Context) *Test {
		d1 := dag.Directory().WithNewFile("foo", "FOO\n")
		d2 := dag.Directory().WithNewFile("bar", "BAR\n")

		return &Test{
			Ctr: dag.Container().
				From("%s").
				WithEnvVariable("COOLENV", "woo").
				WithWorkdir("/coolworkdir").
				WithMountedDirectory("/a_mnt", d1).
				WithMountedCache("/cachemnt", dag.CacheVolume("whateverbrah")).
				WithMountedDirectory("/z_mnt", d2).
				WithDefaultTerminalCmd([]string{"/bin/sh"}),
		}
	}

	type Test struct {
		Ctr *dagger.Container
	}
	`, alpineImage)), 0644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		// cache the module load itself so there's less to wait for in the shell invocation below
		_, err = hostDaggerExec(ctx, t, modDir, "functions")
		require.NoError(t, err)

		// timeout for waiting for each expected line is very generous in case CI is under heavy load or something
		console, err := newTUIConsole(t, 60*time.Second)
		require.NoError(t, err)
		defer console.Close()

		tty := console.Tty()

		// We want the size to be big enough to fit the output we're expecting, but increasing
		// the size also eventually slows down the tests due to more output being generated and
		// needing parsing.
		err = pty.Setsize(tty, &pty.Winsize{Rows: 6, Cols: 16})
		require.NoError(t, err)

		cmd := hostDaggerCommand(ctx, t, modDir, "call", "ctr", "terminal")
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		err = cmd.Start()
		require.NoError(t, err)

		prompt := fmt.Sprintf("/coolworkdir%s $ ", resetSeq)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("pwd")
		require.NoError(t, err)

		_, err = console.ExpectString("/coolworkdir\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("cat /a_mnt/foo")
		require.NoError(t, err)

		_, err = console.ExpectString("FOO\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("stat /cachemnt")
		require.NoError(t, err)

		_, err = console.ExpectString("File: /cachemnt\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("cat /z_mnt/bar")
		require.NoError(t, err)

		_, err = console.ExpectString("BAR\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("echo $COOLENV")
		require.NoError(t, err)

		_, err = console.ExpectString("woo\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("exit")
		require.NoError(t, err)

		go console.ExpectEOF()

		err = cmd.Wait()
		require.NoError(t, err)
	})

	t.Run("attachable", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		err := os.WriteFile(filepath.Join(modDir, "main.go"), []byte(fmt.Sprintf(`package main

	import (
		"context"
		"dagger/test/internal/dagger"
	)

	func New(ctx context.Context) *Test {
		return &Test{
			Ctr: dag.Container().
				From("%s").
				WithEnvVariable("COOLENV", "woo").
				WithWorkdir("/coolworkdir").
				WithDefaultTerminalCmd([]string{"/bin/sh"}),
		}
	}

	type Test struct {
		Ctr *dagger.Container
	}

	func (t *Test) Debug() *dagger.Container {
		return t.Ctr.WithEnvVariable("COOLENV", "xoo").Terminal().WithEnvVariable("COOLENV", "yoo")
	}
	`, alpineImage)), 0644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		// cache the module load itself so there's less to wait for in the shell invocation below
		_, err = hostDaggerExec(ctx, t, modDir, "functions")
		require.NoError(t, err)

		// timeout for waiting for each expected line is very generous in case CI is under heavy load or something
		console, err := newTUIConsole(t, 60*time.Second)
		require.NoError(t, err)
		defer console.Close()

		tty := console.Tty()

		// We want the size to be big enough to fit the output we're expecting, but increasing
		// the size also eventually slows down the tests due to more output being generated and
		// needing parsing.
		err = pty.Setsize(tty, &pty.Winsize{Rows: 6, Cols: 16})
		require.NoError(t, err)

		cmd := hostDaggerCommand(ctx, t, modDir, "call", "debug")
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		err = cmd.Start()
		require.NoError(t, err)

		prompt := fmt.Sprintf("/coolworkdir%s $ ", resetSeq)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("pwd")
		require.NoError(t, err)

		_, err = console.ExpectString("/coolworkdir\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("echo $COOLENV")
		require.NoError(t, err)

		_, err = console.ExpectString("xoo\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("exit")
		require.NoError(t, err)

		go console.ExpectEOF()

		err = cmd.Wait()
		require.NoError(t, err)
	})

	t.Run("override args", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		err := os.WriteFile(filepath.Join(modDir, "main.go"), []byte(fmt.Sprintf(`package main
	import (
		"context"
		"dagger/test/internal/dagger"
	)

	func New(ctx context.Context) *Test {
		return &Test{
			Ctr: dag.Container().
				From("%s").
				WithEnvVariable("COOLENV", "woo").
				WithWorkdir("/coolworkdir").
				WithExec([]string{"apk", "add", "python3"}).
				WithDefaultTerminalCmd([]string{"/bin/sh"}),
		}
	}

	type Test struct {
		Ctr *dagger.Container
	}
	`, alpineImage)), 0644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		// cache the returned container so there's less to wait for in the shell invocation below
		_, err = hostDaggerExec(ctx, t, modDir, "call", "ctr", "sync")
		require.NoError(t, err)

		console, err := newTUIConsole(t, 60*time.Second)
		require.NoError(t, err)
		defer console.Close()

		tty := console.Tty()

		err = pty.Setsize(tty, &pty.Winsize{Rows: 5, Cols: 22})
		require.NoError(t, err)

		cmd := hostDaggerCommand(ctx, t, modDir, "call", "ctr", "terminal", "--cmd=python")
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		err = cmd.Start()
		require.NoError(t, err)

		_, err = console.ExpectString(">>> ")
		require.NoError(t, err)

		_, err = console.SendLine("import os")
		require.NoError(t, err)

		_, err = console.ExpectString(">>> ")
		require.NoError(t, err)

		_, err = console.SendLine("os.environ['COOLENV']")
		require.NoError(t, err)

		_, err = console.ExpectString("'woo'")
		require.NoError(t, err)

		_, err = console.ExpectString(">>> ")
		require.NoError(t, err)

		_, err = console.SendLine("exit()")
		require.NoError(t, err)

		go console.ExpectEOF()

		err = cmd.Wait()
		require.NoError(t, err)
	})

	t.Run("nested client", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		err := os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main
	import (
		"context"
		"dagger/test/internal/dagger"
	)
	func New(ctx context.Context, nestedSrc *dagger.Directory) *Test {
		return &Test{
			Ctr: dag.Container().
				From("`+golangImage+`").
				WithMountedDirectory("/src", nestedSrc).
				WithWorkdir("/src").
				WithDefaultTerminalCmd([]string{"go", "run", "."}),
		}
	}
	type Test struct {
		Ctr *dagger.Container
	}
	 `), 0644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		// cache the module load itself so there's less to wait for in the shell invocation below
		_, err = hostDaggerExec(ctx, t, modDir, "functions")
		require.NoError(t, err)

		thisRepoPath, err := filepath.Abs("../..")
		require.NoError(t, err)

		nestedSrcDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(nestedSrcDir, "sdk/go"), 0755))
		require.NoError(t, fs.CopyDir(
			filepath.Join(nestedSrcDir, "sdk/go"),
			filepath.Join(thisRepoPath, "sdk/go"),
		))
		require.NoError(t, fs.CopyFile(
			filepath.Join(nestedSrcDir, "go.mod"),
			filepath.Join(thisRepoPath, "go.mod"),
		))
		require.NoError(t, fs.CopyFile(
			filepath.Join(nestedSrcDir, "go.sum"),
			filepath.Join(thisRepoPath, "go.sum"),
		))
		require.NoError(t, os.WriteFile(filepath.Join(nestedSrcDir, "main.go"), []byte(`package main
	import (
		"context"
		"fmt"

		"dagger.io/dagger"
	)

	func main() {
		_, err := dagger.Connect(context.Background())
		if err != nil {
			panic(err)
		}
		fmt.Println("it worked?")
	}
	`), 0644))

		// timeout for waiting for each expected line is very generous in case CI is under heavy load or something
		console, err := newTUIConsole(t, 60*time.Second)
		require.NoError(t, err)
		defer console.Close()

		tty := console.Tty()

		err = pty.Setsize(tty, &pty.Winsize{Rows: 6, Cols: 41})
		require.NoError(t, err)

		cmd := hostDaggerCommand(ctx, t, modDir, "call", "--nested-src", nestedSrcDir, "ctr", "terminal", "--experimental-privileged-nesting")
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		err = cmd.Start()
		require.NoError(t, err)

		_, err = console.ExpectString("it worked?")
		require.NoError(t, err)

		go console.ExpectEOF()

		err = cmd.Wait()
		require.NoError(t, err)
	})

	t.Run("directory", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		err := os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main
import (
	"context"
	"dagger/test/internal/dagger"
)

func New(ctx context.Context) *Test {
	return &Test{
		Dir: dag.
			Directory().
			WithNewFile("test", "hello world\n"),
	}
}

type Test struct {
	Dir *dagger.Directory
}
`), 0644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		// cache the module load itself so there's less to wait for in the shell invocation below
		_, err = hostDaggerExec(ctx, t, modDir, "functions")
		require.NoError(t, err)

		// timeout for waiting for each expected line is very generous in case CI is under heavy load or something
		console, err := newTUIConsole(t, 60*time.Second)
		require.NoError(t, err)
		defer console.Close()

		tty := console.Tty()

		// We want the size to be big enough to fit the output we're expecting, but increasing
		// the size also eventually slows down the tests due to more output being generated and
		// needing parsing.
		err = pty.Setsize(tty, &pty.Winsize{Rows: 6, Cols: 16})
		require.NoError(t, err)

		cmd := hostDaggerCommand(ctx, t, modDir, "call", "dir", "terminal")
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		err = cmd.Start()
		require.NoError(t, err)

		prompt := fmt.Sprintf("/src%s $ ", resetSeq)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("cat test")
		require.NoError(t, err)

		_, err = console.ExpectString("hello world\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("exit")
		require.NoError(t, err)

		go console.ExpectEOF()

		err = cmd.Wait()
		require.NoError(t, err)
	})

	t.Run("on failure", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		err := os.WriteFile(filepath.Join(modDir, "main.go"), []byte(fmt.Sprintf(`package main
	import (
		"context"
		"dagger/test/internal/dagger"
	)

	func New(ctx context.Context) *Test {
		d1 := dag.Directory().WithNewFile("foo", "FOO\n")
		d2 := dag.Directory().WithNewFile("bar", "BAR\n")

		return &Test{
			Ctr: dag.Container().
				From("%s").
				WithMountedDirectory("/a_mnt", d1).
				WithMountedCache("/cachemnt", dag.CacheVolume("somethingoranother")).
				WithMountedDirectory("/z_mnt", d2).
				WithExec([]string{"sh", "-c", 
					"echo breakpoint > /fail && echo FOOFOO > /a_mnt/foo && echo BARBAR > /z_mnt/bar && exit 42",
				}),
		}
	}

	type Test struct {
		Ctr *dagger.Container
	}
	`, alpineImage)), 0644)
		require.NoError(t, err)

		_, err = hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		// cache the module load itself so there's less to wait for in the shell invocation below
		_, err = hostDaggerExec(ctx, t, modDir, "functions")
		require.NoError(t, err)

		// timeout for waiting for each expected line is very generous in case CI is under heavy load or something
		console, err := newTUIConsole(t, 60*time.Second)
		require.NoError(t, err)
		defer console.Close()

		tty := console.Tty()

		// We want the size to be big enough to fit the output we're expecting, but increasing
		// the size also eventually slows down the tests due to more output being generated and
		// needing parsing.
		err = pty.Setsize(tty, &pty.Winsize{Rows: 6, Cols: 16})
		require.NoError(t, err)

		cmd := hostDaggerCommand(ctx, t, modDir, "--interactive", "call", "ctr")
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		err = cmd.Start()
		require.NoError(t, err)

		prompt := "/ # "

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("cat /fail")
		require.NoError(t, err)

		_, err = console.ExpectString("breakpoint\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("cat /a_mnt/foo")
		require.NoError(t, err)

		_, err = console.ExpectString("FOOFOO\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("stat /cachemnt")
		require.NoError(t, err)

		_, err = console.ExpectString("File: /cachemnt\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("cat /z_mnt/bar")
		require.NoError(t, err)

		_, err = console.ExpectString("BARBAR\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("exit")
		require.NoError(t, err)

		go console.ExpectEOF()

		err = cmd.Wait()
		require.Error(t, err)

		// We try again with an invalid shell to confirm we replaced the default command
		// We have to set a TTY though or else the error will just be that the --interactive flag doesn't work without a terminal
		console, err = newTUIConsole(t, 60*time.Second)
		require.NoError(t, err)
		defer console.Close()
		tty = console.Tty()
		err = pty.Setsize(tty, &pty.Winsize{Rows: 6, Cols: 16})
		require.NoError(t, err)
		cmd = hostDaggerCommand(ctx, t, modDir, "--interactive", "--interactive-command", "/bin/noexist", "call", "ctr")
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		err = cmd.Start()
		require.NoError(t, err)

		_, err = console.ExpectString("/bin/noexist: no such file or directory")
		require.NoError(t, err)

		err = cmd.Wait()
		require.Error(t, err)
	})
}

// tuiConsole wraps expect.Console with methods that allow us to enforce
// timeouts despite the fact that the TUI is constantly writing more data
// (which invalidates the expect lib's builtin read timeout mechanisms).
type tuiConsole struct {
	*expect.Console
	expectLineTimeout time.Duration
	output            *bytes.Buffer
}

func newTUIConsole(t *testctx.T, expectLineTimeout time.Duration) (*tuiConsole, error) {
	output := bytes.NewBuffer(nil)
	console, err := expect.NewConsole(
		expect.WithStdout(io.MultiWriter(testutil.NewTWriter(t), output)),
		expect.WithDefaultTimeout(expectLineTimeout),
	)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		console.Close()
	})
	return &tuiConsole{
		Console:           console,
		expectLineTimeout: expectLineTimeout,
		output:            output,
	}, nil
}

func (e *tuiConsole) MatchLine(ctx context.Context, pattern string) (string, []string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, e.expectLineTimeout)
	defer cancel()
	lineMatcher := expect.RegexpPattern(".*\n")
	for {
		select {
		case <-ctx.Done():
			return "", nil, fmt.Errorf("timed out waiting for line matching %q, most recent output:\n%s", pattern, e.output.String())
		default:
		}

		line, err := e.Expect(lineMatcher)
		if err != nil {
			return "", nil, err
		}
		if matches := re.FindStringSubmatch(line); matches != nil {
			return line, matches, nil
		}
	}
}
