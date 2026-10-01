package gitutil

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestRunWithStdin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	blob := func(data string) string {
		sum := sha1.Sum(fmt.Appendf(nil, "blob %d\x00%s", len(data), data))
		return hex.EncodeToString(sum[:])
	}
	hash := func(t *testing.T, out []byte, err error) string {
		t.Helper()
		require.NoError(t, err)
		return strings.TrimSpace(string(out))
	}

	var execs int
	cli := NewGitCLI(WithDir(t.TempDir()), WithExec(func(_ context.Context, cmd *exec.Cmd) error {
		execs++
		return cmd.Run()
	}))
	// Binary input, NULs and newlines included, reaches the exec hook's command.
	data := "a\x00b\n-c,\xe2\x98\x83"
	out, err := cli.RunWithStdin(ctx, strings.NewReader(data), "hash-object", "--stdin")
	require.Equal(t, blob(data), hash(t, out, err))
	require.Equal(t, 1, execs)

	// Stdin belongs to one invocation: neither Run on the same CLI nor a
	// derived CLI sees it.
	out, err = cli.Run(ctx, "hash-object", "--stdin")
	require.Equal(t, blob(""), hash(t, out, err))
	out, err = cli.New(WithArgs("-c", "core.autocrlf=false")).Run(ctx, "hash-object", "--stdin")
	require.Equal(t, blob(""), hash(t, out, err))
	require.Equal(t, 3, execs)

	// Without an exec hook, and with streams.
	var stdout strings.Builder
	streams := NewGitCLI(WithStreams(func(context.Context) (io.WriteCloser, io.WriteCloser, func()) {
		return nopWriteCloser{&stdout}, nopWriteCloser{io.Discard}, func() {}
	}))
	out, err = streams.RunWithStdin(ctx, strings.NewReader("hello\n"), "hash-object", "--stdin")
	require.Equal(t, blob("hello\n"), hash(t, out, err))
	require.Equal(t, blob("hello\n"), strings.TrimSpace(stdout.String()))
}
