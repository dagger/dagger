package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	dagger "github.com/dagger/dagger/internal/testutil/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// tWriter is a writer that writes to testing.T
type tWriter struct {
	t   testing.TB
	buf bytes.Buffer
	mu  sync.Mutex
}

// NewTWriter creates a new TWriter
func NewTWriter(t testing.TB) io.Writer {
	tw := &tWriter{t: t}
	t.Cleanup(tw.flush)
	return tw
}

// Write writes data to the testing.T
func (tw *tWriter) Write(p []byte) (n int, err error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	tw.t.Helper()

	if n, err = tw.buf.Write(p); err != nil {
		return n, err
	}

	for {
		line, err := tw.buf.ReadBytes('\n')
		if err == io.EOF {
			// If we've reached the end of the buffer, write it back, because it doesn't have a newline
			tw.buf.Write(line)
			break
		}
		if err != nil {
			return n, err
		}

		tw.t.Log(strings.TrimSuffix(string(line), "\n"))
	}
	return n, nil
}

func (tw *tWriter) flush() {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	tw.t.Log(tw.buf.String())
}

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
		dagger.WithLogOutput(NewTWriter(t)),
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

const testctxTypeAttr = "dagger.io/testctx.type"
const testctxNameAttr = "dagger.io/testctx.name"
const testctxPrewarmAttr = "dagger.io/testctx.prewarm"

func isPrewarm() bool {
	_, ok := os.LookupEnv("TESTCTX_PREWARM")
	return ok
}

// SpanOpts returns span options for testctx
func SpanOpts[T testctx.Runner[T]](w *testctx.W[T]) []trace.SpanStartOption {
	var t T
	attrs := []attribute.KeyValue{
		attribute.String(testctxNameAttr, w.Name()),
		attribute.String(testctxTypeAttr, fmt.Sprintf("%T", t)),
		// Prevent revealed/rolled-up stuff bubbling up through test spans.
		attribute.Bool(telemetry.UIBoundaryAttr, true),
	}
	if strings.Count(w.Name(), "/") == 0 {
		// Only reveal top-level test suites; we don't need to automatically see
		// every single one.
		attrs = append(attrs, attribute.Bool(telemetry.UIRevealAttr, true))
	}
	if isPrewarm() {
		attrs = append(attrs, attribute.Bool(testctxPrewarmAttr, true))
	}
	return []trace.SpanStartOption{
		trace.WithAttributes(attrs...),
	}
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
