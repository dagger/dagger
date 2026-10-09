package gogenerator

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/stretchr/testify/require"
)

// These tests exercise the Go client generated from these templates, in
// sdk/go/core, against a fake engine connection.

// TestObjectIDFetchedOnce verifies that passing the same object as an
// argument more than once fetches its ID only once.
func TestObjectIDFetchedOnce(t *testing.T) {
	ctx := context.Background()
	conn := &fakeIDConn{}
	c, err := dagger.Connect(ctx, dagger.WithConn(conn))
	require.NoError(t, err)
	q := core.NewQuery(c)

	f := q.Directory().File("a")
	_, err = q.Container().WithFile("/a", f).WithFile("/b", f).ID(ctx)
	require.NoError(t, err)
	_, err = q.Container().WithFile("/c", f).ID(ctx)
	require.NoError(t, err)

	require.Equal(t, 1, conn.count("directory.file.id"))
	require.Contains(t, conn.last("container.withFile.withFile.id"), `source:"directory.file.id"`)
}

// TestObjectIDFetchedOnceConcurrently verifies that concurrent callers share
// one ID fetch.
func TestObjectIDFetchedOnceConcurrently(t *testing.T) {
	ctx := context.Background()
	conn := &fakeIDConn{}
	c, err := dagger.Connect(ctx, dagger.WithConn(conn))
	require.NoError(t, err)
	q := core.NewQuery(c)

	f := q.Directory().File("a")
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			id, err := f.ID(ctx)
			require.NoError(t, err)
			require.Equal(t, core.ID("directory.file.id"), id)
		})
	}
	wg.Wait()

	require.Equal(t, 1, conn.count("directory.file.id"))
}

// TestObjectIDFetchErrorNotRemembered verifies that a failed ID fetch is
// retried by the next caller.
func TestObjectIDFetchErrorNotRemembered(t *testing.T) {
	ctx := context.Background()
	conn := &fakeIDConn{failFirst: "directory.file.id"}
	c, err := dagger.Connect(ctx, dagger.WithConn(conn))
	require.NoError(t, err)
	q := core.NewQuery(c)

	f := q.Directory().File("a")
	_, err = f.ID(ctx)
	require.ErrorContains(t, err, "injected failure")
	id, err := f.ID(ctx)
	require.NoError(t, err)
	require.Equal(t, core.ID("directory.file.id"), id)
	_, err = f.ID(ctx)
	require.NoError(t, err)

	require.Equal(t, 2, conn.count("directory.file.id"))
}

// TestObjectIDRememberedPerObject verifies that the ID is remembered per
// object, not per query text: two objects built by the same calls each fetch
// their own ID.
func TestObjectIDRememberedPerObject(t *testing.T) {
	ctx := context.Background()
	conn := &fakeIDConn{}
	c, err := dagger.Connect(ctx, dagger.WithConn(conn))
	require.NoError(t, err)
	q := core.NewQuery(c)

	_, err = q.Directory().File("a").ID(ctx)
	require.NoError(t, err)
	_, err = q.Directory().File("a").ID(ctx)
	require.NoError(t, err)

	require.Equal(t, 2, conn.count("directory.file.id"))
}

// TestObjectIDWaiterHonorsContext verifies that a caller waiting for another
// caller's ID fetch stops waiting when its own context is done.
func TestObjectIDWaiterHonorsContext(t *testing.T) {
	ctx := context.Background()
	release := make(chan struct{})
	conn := &fakeIDConn{blockFirst: "directory.file.id", release: release}
	c, err := dagger.Connect(ctx, dagger.WithConn(conn))
	require.NoError(t, err)
	q := core.NewQuery(c)

	f := q.Directory().File("a")
	first := make(chan error, 1)
	go func() {
		_, err := f.ID(ctx)
		first <- err
	}()
	require.Eventually(t, func() bool {
		return conn.count("directory.file.id") == 1
	}, 10*time.Second, time.Millisecond)

	waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err = f.ID(waitCtx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	close(release)
	require.NoError(t, <-first)
	id, err := f.ID(ctx)
	require.NoError(t, err)
	require.Equal(t, core.ID("directory.file.id"), id)
	require.Equal(t, 1, conn.count("directory.file.id"))
}

// TestObjectIDRefetchedForReevaluatedChain verifies that an object built
// through a field marked @reevaluate, and every object derived from it, fetch
// their ID on every use, so each use evaluates that field again.
func TestObjectIDRefetchedForReevaluatedChain(t *testing.T) {
	ctx := context.Background()
	conn := &fakeIDConn{}
	c, err := dagger.Connect(ctx, dagger.WithConn(conn))
	require.NoError(t, err)
	q := core.NewQuery(c)

	ctr := q.Address("alpine").Container()
	derived := ctr.WithEnvVariable("A", "B")
	for range 2 {
		_, err = q.Container().WithServiceBinding("a", ctr.AsService()).ID(ctx)
		require.NoError(t, err)
		_, err = derived.ID(ctx)
		require.NoError(t, err)
	}
	_, err = ctr.ID(ctx)
	require.NoError(t, err)
	_, err = ctr.ID(ctx)
	require.NoError(t, err)

	require.Equal(t, 2, conn.count("address.container.asService.id"))
	require.Equal(t, 2, conn.count("address.container.withEnvVariable.id"))
	require.Equal(t, 2, conn.count("address.container.id"))
}

// TestObjectIDRefetchedWhenRequested verifies that an object built through a
// field reevaluated only when an argument asks for it (here noCache) fetches
// its ID on every use only for such calls.
func TestObjectIDRefetchedWhenRequested(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		opts []core.HostDirectoryOpts
		want int
	}{
		{name: "default", want: 1},
		{name: "noCache", opts: []core.HostDirectoryOpts{{NoCache: true}}, want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &fakeIDConn{}
			c, err := dagger.Connect(ctx, dagger.WithConn(conn))
			require.NoError(t, err)
			dir := core.NewQuery(c).Host().Directory(".", tc.opts...)
			for range 2 {
				_, err := dir.ID(ctx)
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, conn.count("host.directory.id"))
		})
	}
}

// TestObjectIDRefetchedForRawQuery verifies that an object built from a raw
// query, whose fields may need to be reevaluated, fetches its ID on every use.
func TestObjectIDRefetchedForRawQuery(t *testing.T) {
	ctx := context.Background()
	conn := &fakeIDConn{}
	c, err := dagger.Connect(ctx, dagger.WithConn(conn))
	require.NoError(t, err)
	q := core.NewQuery(c)

	f := (&core.File{}).WithGraphQLQuery(q.QueryBuilder().Select("directory").Select("file").Arg("path", "a"))
	for range 2 {
		_, err := f.ID(ctx)
		require.NoError(t, err)
	}
	require.Equal(t, 2, conn.count("directory.file.id"))
}

// fakeIDConn answers single-chain GraphQL queries. It returns the chain's
// field path, such as "directory.file.id", as the value of the last field, and
// records each query by that path.
type fakeIDConn struct {
	mu        sync.Mutex
	queries   map[string][]string
	failFirst string
	// blockFirst names a path whose first query waits until release is
	// closed.
	blockFirst string
	release    chan struct{}
}

func (c *fakeIDConn) Host() string { return "fake" }
func (c *fakeIDConn) Close() error { return nil }

func (c *fakeIDConn) Do(req *http.Request) (*http.Response, error) {
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, err
	}
	fields := queryFieldPath(body.Query)
	path := strings.Join(fields, ".")

	c.mu.Lock()
	if c.queries == nil {
		c.queries = map[string][]string{}
	}
	c.queries[path] = append(c.queries[path], body.Query)
	fail := path == c.failFirst && len(c.queries[path]) == 1
	block := path == c.blockFirst && len(c.queries[path]) == 1
	c.mu.Unlock()
	if block {
		<-c.release
	}

	var resp any
	if fail {
		resp = map[string]any{"errors": []any{map[string]any{"message": "injected failure"}}}
	} else {
		var data any = path
		for i := len(fields) - 1; i >= 0; i-- {
			data = map[string]any{fields[i]: data}
		}
		resp = map[string]any{"data": data}
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(out)),
	}, nil
}

func (c *fakeIDConn) count(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queries[path])
}

func (c *fakeIDConn) last(path string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	qs := c.queries[path]
	if len(qs) == 0 {
		return ""
	}
	return qs[len(qs)-1]
}

// queryFieldPath returns the field names of a single-chain query such as
// `query{a{b(x:"y"){c}}}`, skipping arguments and quoted strings.
func queryFieldPath(q string) []string {
	if i := strings.IndexByte(q, '{'); i >= 0 {
		q = q[i:]
	}
	var fields []string
	var name strings.Builder
	depth := 0
	inString := false
	for i := 0; i < len(q); i++ {
		ch := q[i]
		if inString {
			switch ch {
			case '\\':
				i++
			case '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '(':
			depth++
		case ')':
			depth--
		case '{', '}', ' ':
			if depth == 0 && name.Len() > 0 {
				fields = append(fields, name.String())
				name.Reset()
			}
		default:
			if depth == 0 {
				name.WriteByte(ch)
			}
		}
	}
	return fields
}
