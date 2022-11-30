package main

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestAllowedLocalDirs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	allowedDir1 := t.TempDir()
	allowedDir2 := t.TempDir()
	allowedDir3 := t.TempDir()
	notAllowedDir := t.TempDir()

	cmd := rootCmd
	cmd.SetArgs([]string{
		"listen",
		"--listen", "localhost:0",
		"--local-dirs", strings.Join([]string{allowedDir1, allowedDir2}, ","),
		"--local-dirs", allowedDir3,
	})

	r, w := io.Pipe()
	cmd.SetOut(w)

	go func() {
		if err := cmd.ExecuteContext(ctx); err != nil {
			panic(err)
		}
	}()

	outCh := make(chan string)
	go func() {
		defer close(outCh)
		out, err := bufio.NewReader(r).ReadString('\n')
		require.NoError(t, err)
		outCh <- out
	}()

	var addr string
	select {
	case out := <-outCh:
		var ok bool
		_, addr, ok = strings.Cut(out, outputPrefix)
		require.True(t, ok, "expected output to start with %q: %q", outputPrefix, out)
		addr = strings.TrimSpace(addr)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for output")
	}

	origDaggerHost := os.Getenv("DAGGER_HOST")
	os.Setenv("DAGGER_HOST", "http://"+addr)
	defer os.Setenv("DAGGER_HOST", origDaggerHost)

	c, err := dagger.Connect(ctx)
	require.NoError(t, err)

	for _, allowedDir := range []string{allowedDir1, allowedDir2, allowedDir3} {
		_, err := c.Host().Directory(allowedDir).Entries(ctx)
		require.NoError(t, err)
	}

	_, err = c.Host().Directory(notAllowedDir).Entries(ctx)
	require.ErrorContains(t, err, "no access allowed")
}
