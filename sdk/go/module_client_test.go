package dagger

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type testGraphQLClient func(context.Context, *graphql.Request, *graphql.Response) error

func (fn testGraphQLClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	return fn(ctx, req, resp)
}

func TestModuleClientSharesLoadsByConnectionAndPin(t *testing.T) {
	var loads atomic.Int32
	client := &Client{client: testGraphQLClient(func(ctx context.Context, req *graphql.Request, _ *graphql.Response) error {
		if req.OpName == "LoadModule" {
			loads.Add(1)
			require.Equal(t, "github.com/example/module", req.Variables.(map[string]any)["address"])
		}
		return nil
	})}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			require.NoError(t, ModuleGraphQLClient(client, "github.com/example/module", "one").MakeRequest(t.Context(), &graphql.Request{}, &graphql.Response{}))
		})
	}
	wg.Wait()
	require.EqualValues(t, 1, loads.Load())
	require.NoError(t, ModuleGraphQLClient(client, "github.com/example/module", "two").MakeRequest(t.Context(), &graphql.Request{}, &graphql.Response{}))
	require.EqualValues(t, 2, loads.Load())
	other := &Client{client: client.client}
	require.NoError(t, ModuleGraphQLClient(other, "github.com/example/module", "one").MakeRequest(t.Context(), &graphql.Request{}, &graphql.Response{}))
	require.EqualValues(t, 3, loads.Load())
}

func TestModuleClientRetriesFailedLoad(t *testing.T) {
	sentinel := errors.New("temporary failure")
	calls := 0
	client := &Client{client: testGraphQLClient(func(_ context.Context, req *graphql.Request, _ *graphql.Response) error {
		if req.OpName == "LoadModule" {
			calls++
			if calls == 1 {
				return sentinel
			}
		}
		return nil
	})}
	transport := ModuleGraphQLClient(client, "/hello", "")
	require.ErrorIs(t, transport.MakeRequest(t.Context(), &graphql.Request{}, &graphql.Response{}), sentinel)
	require.NoError(t, transport.MakeRequest(t.Context(), &graphql.Request{}, &graphql.Response{}))
	require.Equal(t, 2, calls)
	require.NoError(t, client.Close())
	require.ErrorIs(t, transport.MakeRequest(t.Context(), &graphql.Request{}, &graphql.Response{}), ErrClientClosed)
}

func TestModuleClientCancelledWaitDoesNotCancelLoad(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	client := &Client{client: testGraphQLClient(func(ctx context.Context, req *graphql.Request, _ *graphql.Response) error {
		if req.OpName == "LoadModule" {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})}
	transport := ModuleGraphQLClient(client, "/hello", "")
	done := make(chan error, 1)
	go func() { done <- transport.MakeRequest(t.Context(), &graphql.Request{}, &graphql.Response{}) }()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, transport.MakeRequest(ctx, &graphql.Request{}, &graphql.Response{}), context.Canceled)
	close(release)
	require.NoError(t, <-done)
	require.NoError(t, transport.MakeRequest(t.Context(), &graphql.Request{}, &graphql.Response{}))
}

func TestDefaultTransportsAreLazyAndFollowConnectionLifecycle(t *testing.T) {
	require.NoError(t, Close())
	t.Cleanup(func() { require.NoError(t, Close()) })
	transport := ModuleGraphQLClient(nil, "/hello", "")
	raw := DefaultGraphQLClient()
	require.Nil(t, defaultClient)
	calls := 0
	newClient := func() *Client {
		return &Client{client: testGraphQLClient(func(context.Context, *graphql.Request, *graphql.Response) error { calls++; return nil })}
	}
	defaultClient = newClient()
	first := defaultClient
	require.NoError(t, transport.MakeRequest(t.Context(), &graphql.Request{}, &graphql.Response{}))
	require.NoError(t, raw.MakeRequest(t.Context(), &graphql.Request{}, &graphql.Response{}))
	require.Equal(t, 3, calls)
	require.NoError(t, Close())
	require.True(t, first.isClosed())
	defaultClient = newClient()
	require.NoError(t, transport.MakeRequest(t.Context(), &graphql.Request{}, &graphql.Response{}))
	require.Equal(t, 5, calls, "a new connection must load the module again")
}

func TestModuleClientClassifiesOnlyMissingFieldValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		stale bool
	}{
		{"missing binding", &gqlerror.Error{Message: `Cannot query field "hi" on type "Hello".`, Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"}}, true},
		{"resolver quotes validator", &gqlerror.Error{Message: `Cannot query field "hi" on type "Hello".`, Path: ast.Path{ast.PathName("hello")}, Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"}}, false},
		{"ordinary error", errors.New(`Cannot query field "hi" on type "Hello".`), false},
		{"other validation", &gqlerror.Error{Message: "Unknown argument", Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &Client{client: testGraphQLClient(func(_ context.Context, req *graphql.Request, _ *graphql.Response) error {
				if req.OpName == "LoadModule" {
					return nil
				}
				return tc.err
			})}
			err := ModuleGraphQLClient(client, "/hello", "").MakeRequest(t.Context(), &graphql.Request{}, &graphql.Response{})
			require.ErrorIs(t, err, tc.err)
			if tc.stale {
				require.Contains(t, err.Error(), "regenerate")
			} else {
				require.NotContains(t, err.Error(), "regenerate")
			}
		})
	}
}
