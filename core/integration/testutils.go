package core

import (
	"context"
	"fmt"
	"strings"
	"sync"

	dagger "github.com/dagger/dagger/internal/testutil/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// QueryOptions contains options for Query
type QueryOptions struct {
	Operation string
	Variables map[string]any
	Secrets   map[string]string
}

// Query executes a GraphQL query and returns the result
func Query[R any](t *testctx.T, query string, opts *QueryOptions, clientOpts ...dagger.ClientOpt) (*R, error) {
	t.Helper()
	ctx := t.Context()
	clientOpts = append([]dagger.ClientOpt{
		dagger.WithLogOutput(testutil.NewTWriter(t)),
	}, clientOpts...)
	client, err := dagger.Connect(ctx, clientOpts...)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { client.Close() })

	return QueryWithClient[R](client, t, query, opts)
}

// QueryWithClient executes a GraphQL query with an existing generated client
func QueryWithClient[R any](c *dagger.Client, t *testctx.T, query string, opts *QueryOptions) (*R, error) {
	t.Helper()
	ctx := t.Context()

	if opts == nil {
		opts = &QueryOptions{}
	}
	if opts.Variables == nil {
		opts.Variables = make(map[string]any)
	}
	if opts.Secrets == nil {
		opts.Secrets = make(map[string]string)
	}
	for n, v := range opts.Secrets {
		s, err := newSecret(ctx, c, n, v)
		if err != nil {
			return nil, err
		}
		opts.Variables[n] = s
	}

	// Use the generated client's Do method to execute GraphQL in the same session
	r := new(R)
	err := c.Do(ctx,
		&dagger.Request{
			Query:     query,
			Variables: opts.Variables,
			OpName:    opts.Operation,
		},
		&dagger.Response{Data: r},
	)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func newSecret(ctx context.Context, c *dagger.Client, name, value string) (*dagger.SecretID, error) {
	secret := c.SetSecret(name, value)
	id, err := secret.ID(ctx)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// HasPrefix tests that s starts with expectedPrefix
func HasPrefix(t require.TestingT, expectedPrefix, s string, msgAndArgs ...interface{}) {
	if strings.HasPrefix(s, expectedPrefix) {
		return
	}
	require.Fail(t, fmt.Sprintf("Missing prefix: \n"+
		"expected : %s\n"+
		"in string: %s", expectedPrefix, s), msgAndArgs...)
}

var (
	nestedEngineCount   uint8
	nestedEngineCountMu sync.Mutex
)

// GetUniqueNestedEngineNetwork returns a device name and cidr to use; enables us to have unique devices+ip ranges for nested
// engine services to prevent conflicts
func GetUniqueNestedEngineNetwork() (deviceName string, cidr string) {
	nestedEngineCountMu.Lock()
	defer nestedEngineCountMu.Unlock()

	cur := nestedEngineCount
	nestedEngineCount++
	if nestedEngineCount == 0 {
		panic("nestedEngineCount overflow")
	}

	return fmt.Sprintf("dagger%d", cur), fmt.Sprintf("10.89.%d.0/24", cur)
}
