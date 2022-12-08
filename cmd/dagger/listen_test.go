package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
)

func TestAllowedLocalDirs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	allowedDir1 := t.TempDir()
	allowedDir2 := t.TempDir()
	allowedDir3 := t.TempDir()
	notAllowedDir := t.TempDir()

	cmd := rootCmd()
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
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for output")
	}

	gql := graphql.NewClient("http://"+addr+"/query", &http.Client{})

	for _, allowedDir := range []string{allowedDir1, allowedDir2, allowedDir3} {
		req := graphql.Request{Query: `{host{directory(path:"` + allowedDir + `"){entries}}}`}
		data := map[string]any{}
		resp := graphql.Response{Data: &data}
		err := gql.MakeRequest(ctx, &req, &resp)
		require.NoError(t, err)
		require.Empty(t, resp.Errors)
	}

	req := graphql.Request{Query: `{host{directory(path:"` + notAllowedDir + `"){entries}}}`}
	data := map[string]any{}
	resp := graphql.Response{Data: &data}
	err := gql.MakeRequest(ctx, &req, &resp)
	require.ErrorContains(t, err, "no access allowed")
}
