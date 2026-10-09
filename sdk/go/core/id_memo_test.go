package core

import (
	"context"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
	"weak"

	dagger "dagger.io/dagger"
	"github.com/dagger/querybuilder"
	"github.com/stretchr/testify/require"
)

// The behavior of remembered IDs is tested in
// cmd/codegen/generator/go/generated_client_id_test.go, which runs in CI.
// This test needs the unexported idMemos map, so it lives here.

// TestObjectIDForgottenAfterGC verifies that a remembered ID is dropped once
// its object is garbage collected.
func TestObjectIDForgottenAfterGC(t *testing.T) {
	ctx := context.Background()
	c, err := dagger.Connect(ctx, dagger.WithConn(idOnlyConn{}))
	require.NoError(t, err)
	q := NewQuery(c)

	files := make([]*File, 100)
	keys := make([]weak.Pointer[querybuilder.Selection], len(files))
	for i := range files {
		files[i] = q.Directory().File("a")
		keys[i] = weak.Make(files[i].query)
		_, err := files[i].ID(ctx)
		require.NoError(t, err)
	}
	remembered := func() int {
		n := 0
		for _, key := range keys {
			if _, ok := idMemos.Load(key); ok {
				n++
			}
		}
		return n
	}
	require.Equal(t, len(files), remembered())
	// Drop the objects: files is not used after this point.
	runtime.KeepAlive(files)

	require.Eventually(t, func() bool {
		runtime.GC()
		return remembered() == 0
	}, 10*time.Second, 10*time.Millisecond)
}

// idOnlyConn answers every query with an ID.
type idOnlyConn struct{}

func (idOnlyConn) Host() string { return "fake" }
func (idOnlyConn) Close() error { return nil }

func (idOnlyConn) Do(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":{"directory":{"file":{"id":"file"}}}}`)),
	}, nil
}
