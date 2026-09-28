package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

// batchFS is an in-memory stand-in for the agent's workspace: its write and
// edit tools are sequential steps that change it, its read tool is pure. It
// logs when each call starts and ends, so tests can assert on scheduling.
type batchFS struct {
	mu    sync.Mutex
	files map[string]string
	log   []string
}

func newBatchFS(files map[string]string) *batchFS {
	if files == nil {
		files = map[string]string{}
	}
	return &batchFS{files: files}
}

func (fs *batchFS) record(event string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.log = append(fs.log, event)
}

func (fs *batchFS) file(path string) (string, bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	contents, ok := fs.files[path]
	return contents, ok
}

// logged wraps a tool func with start/end events named after its "id"
// argument, or the tool name when there is none.
func (fs *batchFS) logged(name string, fn LLMToolFunc) LLMToolFunc {
	return func(ctx context.Context, rawArgs any) (any, error) {
		args, _ := rawArgs.(map[string]any)
		id, _ := args["id"].(string)
		if id == "" {
			id = name
		}
		fs.record("start " + id)
		defer fs.record("end " + id)
		return fn(ctx, args)
	}
}

func (fs *batchFS) tools() []LLMTool {
	str := func(args any, key string) string {
		s, _ := args.(map[string]any)[key].(string)
		return s
	}
	return []LLMTool{
		{
			Name: "write",
			Call: fs.logged("write", func(_ context.Context, args any) (any, error) {
				fs.mu.Lock()
				defer fs.mu.Unlock()
				fs.files[str(args, "path")] = str(args, "contents")
				return "wrote " + str(args, "path"), nil
			}),
		},
		{
			Name: "edit",
			Call: fs.logged("edit", func(_ context.Context, args any) (any, error) {
				fs.mu.Lock()
				defer fs.mu.Unlock()
				path, oldText := str(args, "path"), str(args, "oldText")
				contents, ok := fs.files[path]
				if !ok {
					return nil, fmt.Errorf("%s does not exist", path)
				}
				if n := strings.Count(contents, oldText); n != 1 {
					return nil, fmt.Errorf("search string found %d times", n)
				}
				fs.files[path] = strings.Replace(contents, oldText, str(args, "newText"), 1)
				return "edited " + path, nil
			}),
		},
		{
			Name:     "read",
			ReadOnly: true,
			Call: fs.logged("read", func(_ context.Context, args any) (any, error) {
				if d := str(args, "delay"); d != "" {
					delay, err := time.ParseDuration(d)
					if err != nil {
						return nil, err
					}
					time.Sleep(delay)
				}
				contents, ok := fs.file(str(args, "path"))
				if !ok {
					return nil, fmt.Errorf("%s does not exist", str(args, "path"))
				}
				return contents, nil
			}),
		},
	}
}

// batchCall builds a tool call numbered by its position in the batch.
func batchCall(t *testing.T, i int, name string, args map[string]any) *LLMToolCall {
	t.Helper()
	encoded, err := json.Marshal(args)
	require.NoError(t, err)
	return &LLMToolCall{CallID: fmt.Sprintf("call_%d", i), Name: name, Arguments: JSON(encoded)}
}

type batchResult struct {
	CallID  string
	Text    string
	Errored bool
}

func batchResults(t *testing.T, msgs []*LLMMessage) []batchResult {
	t.Helper()
	out := make([]batchResult, len(msgs))
	for i, msg := range msgs {
		require.NotNil(t, msg, "result %d is missing", i)
		require.Len(t, msg.Content, 1)
		block := msg.Content[0]
		require.Equal(t, LLMContentToolResult, block.Kind)
		out[i] = batchResult{CallID: block.CallID, Text: block.Text, Errored: block.Errored}
	}
	return out
}

func TestCallBatchWriteThenReadSeesTheWrite(t *testing.T) {
	fs := newBatchFS(nil)
	calls := []*LLMToolCall{
		batchCall(t, 1, "write", map[string]any{"path": "a.txt", "contents": "hello"}),
		batchCall(t, 2, "read", map[string]any{"path": "a.txt"}),
	}
	results := batchResults(t, newMCP().CallBatch(t.Context(), fs.tools(), calls, nil, nil))
	require.Equal(t, []batchResult{
		{CallID: "call_1", Text: "wrote a.txt"},
		{CallID: "call_2", Text: "hello"},
	}, results)
}

func TestCallBatchPreservesWriteOrder(t *testing.T) {
	// Each edit's oldText only exists once the edit before it has landed.
	fs := newBatchFS(map[string]string{"a.txt": "one"})
	calls := []*LLMToolCall{
		batchCall(t, 1, "edit", map[string]any{"path": "a.txt", "oldText": "one", "newText": "two"}),
		batchCall(t, 2, "edit", map[string]any{"path": "a.txt", "oldText": "two", "newText": "three"}),
		batchCall(t, 3, "write", map[string]any{"path": "b.txt", "contents": "b"}),
		batchCall(t, 4, "edit", map[string]any{"path": "b.txt", "oldText": "b", "newText": "bee"}),
	}
	results := batchResults(t, newMCP().CallBatch(t.Context(), fs.tools(), calls, nil, nil))
	for _, res := range results {
		require.False(t, res.Errored, "%s: %s", res.CallID, res.Text)
	}
	a, _ := fs.file("a.txt")
	require.Equal(t, "three", a)
	b, _ := fs.file("b.txt")
	require.Equal(t, "bee", b)
}

func TestCallBatchRunsConsecutivePureCallsConcurrently(t *testing.T) {
	const n = 3
	var started sync.WaitGroup
	started.Add(n)
	allStarted := make(chan struct{})
	go func() {
		started.Wait()
		close(allStarted)
	}()
	tools := []LLMTool{{
		Name:     "wait",
		ReadOnly: true,
		Call: func(ctx context.Context, _ any) (any, error) {
			// Each call only returns once all of them are running at once.
			started.Done()
			select {
			case <-allStarted:
				return "ok", nil
			case <-time.After(10 * time.Second):
				return nil, errors.New("pure calls did not run concurrently")
			}
		},
	}}
	var calls []*LLMToolCall
	for i := 1; i <= n; i++ {
		calls = append(calls, batchCall(t, i, "wait", nil))
	}
	for _, res := range batchResults(t, newMCP().CallBatch(t.Context(), tools, calls, nil, nil)) {
		require.False(t, res.Errored, "%s: %s", res.CallID, res.Text)
	}
}

func TestCallBatchSequentialStepIsABarrier(t *testing.T) {
	fs := newBatchFS(map[string]string{"a.txt": "a"})
	calls := []*LLMToolCall{
		batchCall(t, 1, "read", map[string]any{"id": "r1", "path": "a.txt", "delay": "20ms"}),
		batchCall(t, 2, "read", map[string]any{"id": "r2", "path": "a.txt", "delay": "10ms"}),
		batchCall(t, 3, "write", map[string]any{"id": "w", "path": "a.txt", "contents": "A"}),
		batchCall(t, 4, "read", map[string]any{"id": "r3", "path": "a.txt"}),
		batchCall(t, 5, "read", map[string]any{"id": "r4", "path": "a.txt"}),
	}
	results := batchResults(t, newMCP().CallBatch(t.Context(), fs.tools(), calls, nil, nil))
	require.Equal(t, []string{"a", "a", "wrote a.txt", "A", "A"}, []string{
		results[0].Text, results[1].Text, results[2].Text, results[3].Text, results[4].Text,
	})

	idx := func(event string) int {
		for i, e := range fs.log {
			if e == event {
				return i
			}
		}
		t.Fatalf("event %q not logged: %v", event, fs.log)
		return -1
	}
	// Everything before the write finished before it started, and nothing
	// after it started before it finished.
	require.Less(t, idx("end r1"), idx("start w"), fs.log)
	require.Less(t, idx("end r2"), idx("start w"), fs.log)
	require.Less(t, idx("end w"), idx("start r3"), fs.log)
	require.Less(t, idx("end w"), idx("start r4"), fs.log)
}

func TestCallBatchReturnsResultsInCallOrder(t *testing.T) {
	fs := newBatchFS(map[string]string{"a.txt": "a"})
	// The pure calls finish in the reverse of the order they were written.
	calls := []*LLMToolCall{
		batchCall(t, 1, "read", map[string]any{"path": "a.txt", "delay": "30ms"}),
		batchCall(t, 2, "read", map[string]any{"path": "a.txt", "delay": "15ms"}),
		batchCall(t, 3, "read", map[string]any{"path": "a.txt"}),
		batchCall(t, 4, "write", map[string]any{"path": "b.txt", "contents": "b"}),
		batchCall(t, 5, "nonexistent", nil),
		batchCall(t, 6, "read", map[string]any{"path": "b.txt"}),
	}
	results := batchResults(t, newMCP().CallBatch(t.Context(), fs.tools(), calls, nil, nil))
	require.Len(t, results, len(calls))
	for i, res := range results {
		require.Equal(t, calls[i].CallID, res.CallID)
	}
}

func TestCallBatchFailedStepDoesNotStopTheBatch(t *testing.T) {
	fs := newBatchFS(map[string]string{"a.txt": "one one"})
	calls := []*LLMToolCall{
		batchCall(t, 1, "write", map[string]any{"path": "b.txt", "contents": "b"}),
		// Ambiguous: "one" occurs twice.
		batchCall(t, 2, "edit", map[string]any{"path": "a.txt", "oldText": "one", "newText": "two"}),
		batchCall(t, 3, "read", map[string]any{"path": "a.txt"}),
		batchCall(t, 4, "write", map[string]any{"path": "c.txt", "contents": "c"}),
	}
	results := batchResults(t, newMCP().CallBatch(t.Context(), fs.tools(), calls, nil, nil))

	require.False(t, results[0].Errored)
	require.True(t, results[1].Errored)
	require.Contains(t, results[1].Text, "search string found 2 times")
	// Every call reports its own outcome: the ones after the failure still
	// ran, and read the tree as the failure left it.
	require.False(t, results[2].Errored)
	require.Equal(t, "one one", results[2].Text)
	require.False(t, results[3].Errored)

	b, _ := fs.file("b.txt")
	require.Equal(t, "b", b)
	c, _ := fs.file("c.txt")
	require.Equal(t, "c", c)
}

func TestCallBatchUnevaluableChangesetFailsItsOwnCall(t *testing.T) {
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass[*Changeset](srv))

	// A lazy changeset that fails once evaluated, as an edit with an ambiguous
	// search string does.
	evalErr := errors.New("evaluate changeset directories: search string found multiple times")
	broken := &Changeset{paths: &changesetPathsMemo{}}
	broken.paths.once.Do(func() {})
	broken.paths.err = evalErr
	broken.paths.done.Store(true)
	changes, err := dagql.NewObjectResultForCall(broken, srv, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "broken-edit",
		Type:        dagql.NewResultCallType(broken.Type()),
	})
	require.NoError(t, err)

	m := newMCP()
	fs := newBatchFS(nil)
	tools := append(fs.tools(), LLMTool{
		Name: "brokenEdit",
		Call: func(ctx context.Context, _ any) (any, error) {
			handled, out, err := m.applyStateReturn(ctx, srv, changes)
			require.True(t, handled)
			return out, err
		},
	})
	calls := []*LLMToolCall{
		batchCall(t, 1, "write", map[string]any{"path": "a.txt", "contents": "a"}),
		batchCall(t, 2, "brokenEdit", nil),
		batchCall(t, 3, "write", map[string]any{"path": "b.txt", "contents": "b"}),
	}
	results := batchResults(t, m.CallBatch(t.Context(), tools, calls, nil, nil))

	require.False(t, results[0].Errored)
	// The evaluation error is the call's own failure — it was never handed to
	// applyChangeset, which would have failed differently here (no workspace
	// is bound).
	require.True(t, results[1].Errored)
	require.Contains(t, results[1].Text, "search string found multiple times")
	require.NotContains(t, results[1].Text, "no workspace bound")
	require.False(t, results[2].Errored)

	a, _ := fs.file("a.txt")
	require.Equal(t, "a", a, "the write before the broken edit stays applied")
	b, _ := fs.file("b.txt")
	require.Equal(t, "b", b, "the write after it still runs")
}

func TestCallBatchRunsContinuationsLast(t *testing.T) {
	fs := newBatchFS(nil)
	tools := append(fs.tools(), LLMTool{
		Name:       "reload",
		ReturnsLLM: true,
		Call: fs.logged("reload", func(context.Context, any) (any, error) {
			return "reloaded", nil
		}),
	})

	t.Run("after every other call, with the turn folded in first", func(t *testing.T) {
		fs.log = nil
		calls := []*LLMToolCall{
			batchCall(t, 1, "reload", nil),
			batchCall(t, 2, "write", map[string]any{"path": "a.txt", "contents": "a"}),
		}
		results := batchResults(t, newMCP().CallBatch(t.Context(), tools, calls, nil, func(context.Context) error {
			fs.record("fold")
			return nil
		}))
		require.Equal(t, []string{"start write", "end write", "fold", "start reload", "end reload"}, fs.log)
		require.Equal(t, []batchResult{
			{CallID: "call_1", Text: "reloaded"},
			{CallID: "call_2", Text: "wrote a.txt"},
		}, results)
	})

	t.Run("even after a failed step", func(t *testing.T) {
		fs.log = nil
		calls := []*LLMToolCall{
			batchCall(t, 1, "edit", map[string]any{"path": "missing.txt", "oldText": "x", "newText": "y"}),
			batchCall(t, 2, "reload", nil),
		}
		var folded bool
		results := batchResults(t, newMCP().CallBatch(t.Context(), tools, calls, nil, func(context.Context) error {
			folded = true
			return nil
		}))
		require.True(t, folded)
		require.True(t, results[0].Errored)
		require.Equal(t, batchResult{CallID: "call_2", Text: "reloaded"}, results[1])
	})

	t.Run("not when the turn can't be folded in", func(t *testing.T) {
		fs.log = nil
		calls := []*LLMToolCall{batchCall(t, 1, "reload", nil)}
		results := batchResults(t, newMCP().CallBatch(t.Context(), tools, calls, nil, func(context.Context) error {
			return errors.New("boom")
		}))
		require.True(t, results[0].Errored)
		require.Contains(t, results[0].Text, "boom")
		require.NotContains(t, fs.log, "start reload")
	})
}

func TestAnnotateMCPSyncFailure(t *testing.T) {
	read := &LLMToolCall{CallID: "read", Name: "read_file"}
	write := &LLMToolCall{CallID: "write", Name: "write_file"}
	plan := []batchStep{
		{calls: []*LLMToolCall{read}, pure: true},
		{calls: []*LLMToolCall{write}},
	}
	fresh := func() map[*LLMToolCall]*LLMContentBlock {
		return map[*LLMToolCall]*LLMContentBlock{
			read:  {Kind: LLMContentToolResult, CallID: "read", Text: "contents"},
			write: {Kind: LLMContentToolResult, CallID: "write", Text: "wrote"},
		}
	}
	syncErr := errors.New("mount failed")

	t.Run("sync failed after the calls ran", func(t *testing.T) {
		results := fresh()
		annotateMCPSyncFailure("fs", plan, results, true, syncErr)
		// The read stands; the write's effect never reached the workspace.
		require.False(t, results[read].Errored)
		require.Equal(t, "contents", results[read].Text)
		require.True(t, results[write].Errored)
		require.Contains(t, results[write].Text, "wrote\n\nWARNING")
		require.Contains(t, results[write].Text, "could not be carried back into the workspace: mount failed")
	})

	t.Run("calls ran unsynced", func(t *testing.T) {
		results := fresh()
		annotateMCPSyncFailure("fs", plan, results, false, syncErr)
		// Both are suspect: the server never saw the workspace.
		for _, call := range []*LLMToolCall{read, write} {
			require.True(t, results[call].Errored, call.CallID)
			require.Contains(t, results[call].Text, "ran against a stale tree")
			require.Contains(t, results[call].Text, "mount failed")
		}
	})
}
