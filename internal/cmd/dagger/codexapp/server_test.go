package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// fakeConversation stands in for a Dagger conversation. Its prompt function
// decides what a turn does: return, block until interrupted, fail, or narrate
// itself through the item stream.
type fakeConversation struct {
	mu         sync.Mutex
	handle     string
	model      string
	provider   string
	prompt     func(ctx context.Context, text string) error
	prompts    []string
	steered    []string
	interrupt  chan struct{}
	reply      string
	diff       string
	exported   int
	usage      TokenUsageBreakdown
	window     int64
	name       string
	history    []Turn
	historyErr error
}

func newFakeConversation(handle string) *fakeConversation {
	return &fakeConversation{
		handle:    handle,
		model:     "test-model",
		provider:  "test",
		interrupt: make(chan struct{}, 1),
		prompt:    func(context.Context, string) error { return nil },
	}
}

func (c *fakeConversation) Model(context.Context) (string, string, error) {
	return c.model, c.provider, nil
}

func (c *fakeConversation) AgentHandle() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handle
}

func (c *fakeConversation) Prompt(ctx context.Context, text string) error {
	c.mu.Lock()
	c.prompts = append(c.prompts, text)
	prompt := c.prompt
	c.mu.Unlock()
	return prompt(ctx, text)
}

func (c *fakeConversation) Steer(text string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.steered = append(c.steered, text)
	return true
}

func (c *fakeConversation) Interrupt() bool {
	select {
	case c.interrupt <- struct{}{}:
	default:
	}
	return true
}

func (c *fakeConversation) LastReply(context.Context) (string, error) { return c.reply, nil }

func (c *fakeConversation) History(context.Context) ([]Turn, error) {
	return c.history, c.historyErr
}

func (c *fakeConversation) PendingDiff(context.Context) (string, error) { return c.diff, nil }

func (c *fakeConversation) ExportChanges(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.exported++
	return nil
}

func (c *fakeConversation) TokenUsage(context.Context) (TokenUsageBreakdown, int64, error) {
	return c.usage, c.window, nil
}

func (c *fakeConversation) SetName(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.name = name
}

func (c *fakeConversation) snapshot() fakeConversation {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fakeConversation{
		prompts:  append([]string{}, c.prompts...),
		steered:  append([]string{}, c.steered...),
		exported: c.exported,
		name:     c.name,
	}
}

type fakeBackend struct {
	mu      sync.Mutex
	info    ServerInfo
	created []ThreadOptions
	convs   []*fakeConversation
	loaded  map[string]*fakeConversation
	saved   []SavedThread
	history map[string][]Turn
	models  []Model
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		info:    ServerInfo{Version: "v0.0.0-test", Cwd: "/work", Home: "/home/me/.config/dagger"},
		loaded:  map[string]*fakeConversation{},
		history: map[string][]Turn{},
	}
}

func (b *fakeBackend) Info() ServerInfo { return b.info }

func (b *fakeBackend) NewThread(_ context.Context, opts ThreadOptions) (Conversation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.created = append(b.created, opts)
	conv := newFakeConversation(fmt.Sprintf("agent-%d", len(b.convs)+1))
	if opts.Model != "" {
		conv.model = opts.Model
	}
	b.convs = append(b.convs, conv)
	return conv, nil
}

func (b *fakeBackend) LoadThread(_ context.Context, threadID string) (Conversation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	conv, ok := b.loaded[threadID]
	if !ok {
		return nil, fmt.Errorf("session %q not found", threadID)
	}
	return conv, nil
}

func (b *fakeBackend) SavedThreads(context.Context) ([]SavedThread, error) {
	return b.saved, nil
}

func (b *fakeBackend) SavedThreadHistory(_ context.Context, threadID string) ([]Turn, error) {
	return b.history[threadID], nil
}

func (b *fakeBackend) Models(context.Context) ([]Model, error) { return b.models, nil }

func (b *fakeBackend) conv(i int) *fakeConversation {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.convs[i]
}

// testClient drives a server over in-memory pipes.
type testClient struct {
	t       *testing.T
	w       io.WriteCloser
	msgs    chan *Message
	nextID  int
	pending []*Message
}

func newTestClient(t *testing.T, backend Backend) (*testClient, *ItemStream) {
	t.Helper()
	clientR, clientW := io.Pipe()
	serverR, serverW := io.Pipe()
	conn := NewConn(clientR, serverW)
	stream := NewItemStream(conn)
	stream.grace = 50 * time.Millisecond
	srv := NewServer(conn, backend, stream)
	srv.drainQuiet = 10 * time.Millisecond
	srv.drainTimeout = 500 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	msgs := make(chan *Message, 1024)
	go func() {
		defer close(msgs)
		scanner := bufio.NewScanner(serverR)
		scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
		for scanner.Scan() {
			var msg Message
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				panic(fmt.Sprintf("server wrote invalid JSON: %q", scanner.Text()))
			}
			msgs <- &msg
		}
	}()

	t.Cleanup(func() {
		clientW.Close()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("server did not stop after the client hung up")
		}
		cancel()
		serverW.Close()
	})
	return &testClient{t: t, w: clientW, msgs: msgs}, stream
}

func (c *testClient) send(line string) {
	c.t.Helper()
	_, err := io.WriteString(c.w, line+"\n")
	require.NoError(c.t, err)
}

func (c *testClient) recv() *Message {
	c.t.Helper()
	select {
	case msg, ok := <-c.msgs:
		require.True(c.t, ok, "server closed the connection")
		return msg
	case <-time.After(5 * time.Second):
		c.t.Fatal("timed out waiting for the server")
		return nil
	}
}

// call sends a request and returns its response, queueing notifications
// that arrive in the meantime.
func (c *testClient) call(method string, params any) (json.RawMessage, *RPCError) {
	c.t.Helper()
	c.nextID++
	id := c.nextID
	req := map[string]any{"id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	data, err := json.Marshal(req)
	require.NoError(c.t, err)
	c.send(string(data))
	want := fmt.Sprint(id)
	for {
		msg := c.recv()
		if msg.Method != "" {
			c.pending = append(c.pending, msg)
			continue
		}
		require.Equal(c.t, want, string(msg.ID), "response for the wrong request")
		return msg.Result, msg.Error
	}
}

// ok is call for requests that must succeed, decoding the result into out.
func (c *testClient) ok(method string, params, out any) {
	c.t.Helper()
	result, rpcErr := c.call(method, params)
	require.Nil(c.t, rpcErr, "%s failed: %v", method, rpcErr)
	if out != nil {
		require.NoError(c.t, json.Unmarshal(result, out))
	}
}

// notification returns the next notification with the given method,
// consuming (and discarding) everything before it.
func (c *testClient) notification(method string, out any) {
	c.t.Helper()
	for {
		var msg *Message
		if len(c.pending) > 0 {
			msg, c.pending = c.pending[0], c.pending[1:]
		} else {
			msg = c.recv()
		}
		require.NotEmpty(c.t, msg.Method, "expected a notification, got a response")
		if msg.Method != method {
			continue
		}
		if out != nil {
			require.NoError(c.t, json.Unmarshal(msg.Params, out))
		}
		return
	}
}

// notifications collects notification methods until one of the given method
// arrives, returning the ordered list (that one included).
func (c *testClient) notificationsUntil(method string) []*Message {
	c.t.Helper()
	var seen []*Message
	for {
		var msg *Message
		if len(c.pending) > 0 {
			msg, c.pending = c.pending[0], c.pending[1:]
		} else {
			msg = c.recv()
		}
		require.NotEmpty(c.t, msg.Method, "expected a notification, got a response")
		seen = append(seen, msg)
		if msg.Method == method {
			return seen
		}
	}
}

func methodsOf(msgs []*Message) []string {
	var methods []string
	for _, m := range msgs {
		methods = append(methods, m.Method)
	}
	return methods
}

func (c *testClient) initialize() {
	c.t.Helper()
	var res InitializeResponse
	c.ok(MethodInitialize, InitializeParams{ClientInfo: ClientInfo{Name: "test-client", Version: "0.1"}}, &res)
	c.send(`{"method":"initialized"}`)
}

func (c *testClient) startThread(params ThreadStartParams) ThreadStartResponse {
	c.t.Helper()
	var res ThreadStartResponse
	c.ok(MethodThreadStart, params, &res)
	return res
}

func TestServerHandshake(t *testing.T) {
	backend := newFakeBackend()
	client, _ := newTestClient(t, backend)

	_, rpcErr := client.call(MethodThreadStart, ThreadStartParams{})
	require.NotNil(t, rpcErr)
	require.Equal(t, int64(CodeInvalidRequest), rpcErr.Code)

	var res InitializeResponse
	client.ok(MethodInitialize, InitializeParams{ClientInfo: ClientInfo{Name: "ide", Version: "9"}}, &res)
	require.Contains(t, res.UserAgent, "dagger/v0.0.0-test")
	require.Contains(t, res.UserAgent, "ide/9")
	require.Equal(t, "/home/me/.config/dagger", res.CodexHome)
	require.NotEmpty(t, res.PlatformOs)
	require.NotEmpty(t, res.PlatformFamily)

	_, rpcErr = client.call(MethodInitialize, InitializeParams{ClientInfo: ClientInfo{Name: "ide", Version: "9"}})
	require.NotNil(t, rpcErr)
	require.Equal(t, int64(CodeInvalidRequest), rpcErr.Code)

	_, rpcErr = client.call("account/read", nil)
	require.NotNil(t, rpcErr)
	require.Equal(t, int64(CodeMethodNotFound), rpcErr.Code)

	// A line that is not a message is dropped, and the connection lives on.
	client.send("{{{")
	client.ok(MethodThreadLoadedList, nil, nil)
}

func TestServerThreadStartAndTurn(t *testing.T) {
	backend := newFakeBackend()
	client, stream := newTestClient(t, backend)
	client.initialize()

	model := "fancy-model"
	instructions := "be terse"
	res := client.startThread(ThreadStartParams{Model: &model, DeveloperInstructions: &instructions})
	require.NotEmpty(t, res.Thread.ID)
	require.Equal(t, res.Thread.ID, res.Thread.SessionID)
	require.Equal(t, "fancy-model", res.Model)
	require.Equal(t, "test", res.ModelProvider)
	require.Equal(t, "/work", res.Cwd)
	require.Equal(t, ApprovalPolicyNever, res.ApprovalPolicy)
	require.Equal(t, SandboxWorkspaceWrite, res.Sandbox.Type)
	require.Equal(t, ThreadStatusIdle, res.Thread.Status.Type)
	require.Equal(t, ThreadSourceAppServer, res.Thread.Source)
	require.Empty(t, res.Thread.Turns)
	require.Equal(t, []ThreadOptions{{ID: res.Thread.ID, Model: "fancy-model", Instructions: "be terse"}}, backend.created)

	var started ThreadStartedNotification
	client.notification(NotifyThreadStarted, &started)
	require.Equal(t, res.Thread.ID, started.Thread.ID)

	// The turn narrates itself through telemetry, as the engine would.
	conv := backend.conv(0)
	conv.diff = "--- a/x\n+++ b/x\n"
	conv.usage = TokenUsageBreakdown{InputTokens: 100, CachedInputTokens: 40, OutputTokens: 20, TotalTokens: 120}
	conv.window = 200000
	conv.prompt = func(ctx context.Context, text string) error {
		span := newTestSpan("LLM response", agentAttrs(conv.AgentHandle())...)
		_ = stream.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.running()})
		_ = stream.Export(ctx, []sdklog.Record{markdown(span.id, "Hi there"), eof(span.id, 1)})
		_ = stream.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span.ended()})
		return nil
	}

	var turn TurnStartResponse
	client.ok(MethodTurnStart, TurnStartParams{ThreadID: res.Thread.ID, Input: []UserInput{TextInput("hello agent")}}, &turn)
	require.NotEmpty(t, turn.Turn.ID)
	require.Equal(t, TurnStatusInProgress, turn.Turn.Status)

	msgs := client.notificationsUntil(NotifyTurnCompleted)
	require.Equal(t, []string{
		NotifyThreadStatusChanged,
		NotifyTurnStarted,
		NotifyItemStarted,   // userMessage
		NotifyItemCompleted, // userMessage
		NotifyItemStarted,   // agentMessage
		NotifyAgentMessageDelta,
		NotifyItemCompleted,
		NotifyTurnDiffUpdated,
		NotifyThreadTokenUsageUpdated,
		NotifyTurnCompleted,
	}, methodsOf(msgs))

	var userStarted ItemStartedNotification
	require.NoError(t, json.Unmarshal(msgs[2].Params, &userStarted))
	require.Equal(t, turn.Turn.ID, userStarted.TurnID)
	userItem, err := json.Marshal(userStarted.Item)
	require.NoError(t, err)
	require.JSONEq(t, fmt.Sprintf(`{"type":"userMessage","id":%q,"content":[{"type":"text","text":"hello agent"}]}`,
		userStarted.Item.(map[string]any)["id"]), string(userItem))

	var delta AgentMessageDeltaNotification
	require.NoError(t, json.Unmarshal(msgs[5].Params, &delta))
	require.Equal(t, "Hi there", delta.Delta)

	var diff TurnDiffUpdatedNotification
	require.NoError(t, json.Unmarshal(msgs[7].Params, &diff))
	require.Equal(t, conv.diff, diff.Diff)

	var usage ThreadTokenUsageUpdatedNotification
	require.NoError(t, json.Unmarshal(msgs[8].Params, &usage))
	require.Equal(t, conv.usage, usage.TokenUsage.Total)
	require.Equal(t, conv.usage, usage.TokenUsage.Last)
	require.Equal(t, int64(200000), *usage.TokenUsage.ModelContextWindow)

	var completed TurnCompletedNotification
	require.NoError(t, json.Unmarshal(msgs[9].Params, &completed))
	require.Equal(t, turn.Turn.ID, completed.Turn.ID)
	require.Equal(t, TurnStatusCompleted, completed.Turn.Status)
	require.Nil(t, completed.Turn.Error)

	client.notification(NotifyThreadStatusChanged, nil)

	snap := conv.snapshot()
	require.Equal(t, []string{"hello agent"}, snap.prompts)
	require.Equal(t, 1, snap.exported, "changes are exported to the checkout at the end of the turn")
	require.Equal(t, "hello agent", snap.name, "the first prompt names the thread")

	// A second turn reports only its own usage as "last".
	conv.usage = TokenUsageBreakdown{InputTokens: 150, CachedInputTokens: 90, OutputTokens: 30, TotalTokens: 180}
	client.ok(MethodTurnStart, TurnStartParams{ThreadID: res.Thread.ID, Input: []UserInput{TextInput("again")}}, &turn)
	client.notification(NotifyThreadTokenUsageUpdated, &usage)
	require.Equal(t, TokenUsageBreakdown{InputTokens: 50, CachedInputTokens: 50, OutputTokens: 10, TotalTokens: 60}, usage.TokenUsage.Last)
	client.notification(NotifyTurnCompleted, &completed)
}

func TestServerSynthesizesReplyWhenTelemetryIsMissing(t *testing.T) {
	backend := newFakeBackend()
	client, _ := newTestClient(t, backend)
	client.initialize()
	res := client.startThread(ThreadStartParams{})
	conv := backend.conv(0)
	conv.reply = "the answer"

	var turn TurnStartResponse
	client.ok(MethodTurnStart, TurnStartParams{ThreadID: res.Thread.ID, Input: []UserInput{TextInput("q")}}, &turn)
	msgs := client.notificationsUntil(NotifyTurnCompleted)
	require.Equal(t, []string{
		NotifyThreadStarted,
		NotifyThreadStatusChanged,
		NotifyTurnStarted,
		NotifyItemStarted,
		NotifyItemCompleted,
		NotifyItemStarted,
		NotifyAgentMessageDelta,
		NotifyItemCompleted,
		NotifyThreadTokenUsageUpdated,
		NotifyTurnCompleted,
	}, methodsOf(msgs))
	var completed ItemCompletedNotification
	require.NoError(t, json.Unmarshal(msgs[7].Params, &completed))
	require.Equal(t, "the answer", completed.Item.(map[string]any)["text"])
	// No diff, no export.
	require.Equal(t, 0, conv.snapshot().exported)
}

func TestServerInterruptAndSteer(t *testing.T) {
	backend := newFakeBackend()
	client, _ := newTestClient(t, backend)
	client.initialize()
	res := client.startThread(ThreadStartParams{})
	conv := backend.conv(0)
	running := make(chan struct{})
	conv.prompt = func(ctx context.Context, text string) error {
		close(running)
		select {
		case <-conv.interrupt:
			return ErrInterrupted
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	var turn TurnStartResponse
	client.ok(MethodTurnStart, TurnStartParams{ThreadID: res.Thread.ID, Input: []UserInput{TextInput("work")}}, &turn)
	<-running

	// Steering the active turn hands the text to the conversation and shows
	// it as a user message.
	var steer TurnSteerResponse
	client.ok(MethodTurnSteer, TurnSteerParams{ThreadID: res.Thread.ID, ExpectedTurnID: turn.Turn.ID, Input: []UserInput{TextInput("also this")}}, &steer)
	require.Equal(t, turn.Turn.ID, steer.TurnID)
	require.Equal(t, []string{"also this"}, conv.snapshot().steered)

	_, rpcErr := client.call(MethodTurnSteer, TurnSteerParams{ThreadID: res.Thread.ID, ExpectedTurnID: "nope", Input: []UserInput{TextInput("x")}})
	require.NotNil(t, rpcErr)
	require.Equal(t, int64(CodeInvalidRequest), rpcErr.Code)

	_, rpcErr = client.call(MethodTurnInterrupt, TurnInterruptParams{ThreadID: res.Thread.ID, TurnID: "nope"})
	require.NotNil(t, rpcErr)
	require.Equal(t, int64(CodeInvalidRequest), rpcErr.Code)

	client.ok(MethodTurnInterrupt, TurnInterruptParams{ThreadID: res.Thread.ID, TurnID: turn.Turn.ID}, nil)
	var completed TurnCompletedNotification
	client.notification(NotifyTurnCompleted, &completed)
	require.Equal(t, turn.Turn.ID, completed.Turn.ID)
	require.Equal(t, TurnStatusInterrupted, completed.Turn.Status)

	// Once the turn is over there is nothing to interrupt.
	_, rpcErr = client.call(MethodTurnInterrupt, TurnInterruptParams{ThreadID: res.Thread.ID, TurnID: turn.Turn.ID})
	require.NotNil(t, rpcErr)
	require.Equal(t, int64(CodeInvalidRequest), rpcErr.Code)
}

func TestServerFailedTurn(t *testing.T) {
	backend := newFakeBackend()
	client, _ := newTestClient(t, backend)
	client.initialize()
	res := client.startThread(ThreadStartParams{})
	conv := backend.conv(0)
	conv.prompt = func(context.Context, string) error { return errors.New("provider exploded") }

	var turn TurnStartResponse
	client.ok(MethodTurnStart, TurnStartParams{ThreadID: res.Thread.ID, Input: []UserInput{TextInput("q")}}, &turn)
	var errNote ErrorNotification
	client.notification(NotifyError, &errNote)
	require.Equal(t, turn.Turn.ID, errNote.TurnID)
	require.Equal(t, "provider exploded", errNote.Error.Message)
	require.False(t, errNote.WillRetry)
	var completed TurnCompletedNotification
	client.notification(NotifyTurnCompleted, &completed)
	require.Equal(t, TurnStatusFailed, completed.Turn.Status)
	require.Equal(t, &TurnError{Message: "provider exploded"}, completed.Turn.Error)
}

func TestServerQueuesTurnsPerThread(t *testing.T) {
	backend := newFakeBackend()
	client, _ := newTestClient(t, backend)
	client.initialize()
	res := client.startThread(ThreadStartParams{})
	conv := backend.conv(0)
	release := make(chan struct{})
	conv.prompt = func(ctx context.Context, text string) error {
		if text == "first" {
			<-release
		}
		return nil
	}

	var first, second, third TurnStartResponse
	client.ok(MethodTurnStart, TurnStartParams{ThreadID: res.Thread.ID, Input: []UserInput{TextInput("first")}}, &first)
	client.ok(MethodTurnStart, TurnStartParams{ThreadID: res.Thread.ID, Input: []UserInput{TextInput("second")}}, &second)
	client.ok(MethodTurnStart, TurnStartParams{ThreadID: res.Thread.ID, Input: []UserInput{TextInput("third")}}, &third)

	// A queued turn can be withdrawn before it starts.
	client.ok(MethodTurnInterrupt, TurnInterruptParams{ThreadID: res.Thread.ID, TurnID: third.Turn.ID}, nil)
	var withdrawn TurnCompletedNotification
	client.notification(NotifyTurnCompleted, &withdrawn)
	require.Equal(t, third.Turn.ID, withdrawn.Turn.ID)
	require.Equal(t, TurnStatusInterrupted, withdrawn.Turn.Status)

	close(release)
	var completed TurnCompletedNotification
	client.notification(NotifyTurnCompleted, &completed)
	require.Equal(t, first.Turn.ID, completed.Turn.ID)
	var started TurnStartedNotification
	client.notification(NotifyTurnStarted, &started)
	require.Equal(t, second.Turn.ID, started.Turn.ID)
	client.notification(NotifyTurnCompleted, &completed)
	require.Equal(t, second.Turn.ID, completed.Turn.ID)
	require.Equal(t, []string{"first", "second"}, conv.snapshot().prompts)
}

func TestServerRejectsUnsupportedInput(t *testing.T) {
	backend := newFakeBackend()
	client, _ := newTestClient(t, backend)
	client.initialize()
	res := client.startThread(ThreadStartParams{})

	_, rpcErr := client.call(MethodTurnStart, TurnStartParams{ThreadID: res.Thread.ID, Input: []UserInput{{Type: "image", URL: "data:..."}}})
	require.NotNil(t, rpcErr)
	require.Equal(t, int64(CodeInvalidParams), rpcErr.Code)

	_, rpcErr = client.call(MethodTurnStart, TurnStartParams{ThreadID: res.Thread.ID, Input: []UserInput{TextInput("  ")}})
	require.NotNil(t, rpcErr)
	require.Equal(t, int64(CodeInvalidParams), rpcErr.Code)

	_, rpcErr = client.call(MethodTurnStart, TurnStartParams{ThreadID: "missing", Input: []UserInput{TextInput("hi")}})
	require.NotNil(t, rpcErr)
	require.Equal(t, int64(CodeInvalidRequest), rpcErr.Code)
}

func TestServerThreadListResumeAndRead(t *testing.T) {
	backend := newFakeBackend()
	older := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	backend.saved = []SavedThread{
		{ID: "saved-new", Name: "Fix the build", CreatedAt: newer},
		{ID: "saved-old", Name: "Write docs", CreatedAt: older},
	}
	savedTurn := Turn{ID: "turn-1", Status: TurnStatusCompleted, Items: []ThreadItem{NewAgentMessageItem("m1", "done")}}
	backend.history["saved-old"] = []Turn{savedTurn}
	resumed := newFakeConversation("agent-resumed")
	resumed.history = []Turn{savedTurn}
	backend.loaded["saved-new"] = resumed

	client, _ := newTestClient(t, backend)
	client.initialize()

	var list ThreadListResponse
	client.ok(MethodThreadList, ThreadListParams{}, &list)
	require.Len(t, list.Data, 2)
	require.Equal(t, "saved-new", list.Data[0].ID)
	require.Equal(t, "Fix the build", *list.Data[0].Name)
	require.Equal(t, ThreadStatusNotLoaded, list.Data[0].Status.Type)
	require.Equal(t, "saved-old", list.Data[1].ID)

	limit := 1
	client.ok(MethodThreadList, ThreadListParams{Limit: &limit}, &list)
	require.Len(t, list.Data, 1)

	var loaded ThreadLoadedListResponse
	client.ok(MethodThreadLoadedList, nil, &loaded)
	require.Empty(t, loaded.Data)

	var res ThreadStartResponse
	client.ok(MethodThreadResume, ThreadResumeParams{ThreadID: "saved-new"}, &res)
	require.Equal(t, "saved-new", res.Thread.ID)
	require.Equal(t, "Fix the build", *res.Thread.Name)
	require.Equal(t, newer.Unix(), res.Thread.CreatedAt)
	require.Len(t, res.Thread.Turns, 1)
	require.Equal(t, "turn-1", res.Thread.Turns[0].ID)

	_, rpcErr := client.call(MethodThreadResume, ThreadResumeParams{ThreadID: "unknown"})
	require.NotNil(t, rpcErr)
	require.Equal(t, int64(CodeInvalidRequest), rpcErr.Code)

	client.ok(MethodThreadLoadedList, nil, &loaded)
	require.Equal(t, []string{"saved-new"}, loaded.Data)

	// Listing shows the loaded thread as loaded.
	client.ok(MethodThreadList, ThreadListParams{}, &list)
	require.Equal(t, ThreadStatusIdle, list.Data[0].Status.Type)

	// Reading an unloaded thread with turns renders its saved history.
	var read ThreadReadResponse
	client.ok(MethodThreadRead, ThreadReadParams{ThreadID: "saved-old", IncludeTurns: true}, &read)
	require.Equal(t, ThreadStatusNotLoaded, read.Thread.Status.Type)
	require.Len(t, read.Thread.Turns, 1)
	client.ok(MethodThreadRead, ThreadReadParams{ThreadID: "saved-old"}, &read)
	require.Empty(t, read.Thread.Turns)

	var unsub ThreadUnsubscribeResponse
	client.ok(MethodThreadUnsubscribe, ThreadUnsubscribeParams{ThreadID: "saved-old"}, &unsub)
	require.Equal(t, UnsubscribeStatusNotLoaded, unsub.Status)
	client.ok(MethodThreadUnsubscribe, ThreadUnsubscribeParams{ThreadID: "saved-new"}, &unsub)
	require.Equal(t, UnsubscribeStatusUnsubscribed, unsub.Status)

	client.ok(MethodThreadNameSet, ThreadSetNameParams{ThreadID: "saved-new", Name: "Renamed"}, nil)
	var renamed ThreadNameUpdatedNotification
	client.notification(NotifyThreadNameUpdated, &renamed)
	require.Equal(t, "Renamed", *renamed.ThreadName)
	require.Equal(t, "Renamed", resumed.snapshot().name)
}

func TestServerModelListAndOptOut(t *testing.T) {
	backend := newFakeBackend()
	backend.models = []Model{{ID: "m1", Model: "m1", DisplayName: "M1", IsDefault: true, DefaultReasoningEffort: "none",
		SupportedReasoningEfforts: []ReasoningEffortOption{}, InputModalities: []string{"text"}}}
	client, _ := newTestClient(t, backend)
	var res InitializeResponse
	client.ok(MethodInitialize, InitializeParams{
		ClientInfo:   ClientInfo{Name: "ide", Version: "1"},
		Capabilities: &InitializeCapabilities{OptOutNotificationMethods: []string{NotifyThreadStarted, NotifyThreadStatusChanged}},
	}, &res)

	var models ModelListResponse
	client.ok(MethodModelList, ModelListParams{}, &models)
	require.Equal(t, backend.models, models.Data)
	require.Nil(t, models.NextCursor)

	thread := client.startThread(ThreadStartParams{})
	var turn TurnStartResponse
	client.ok(MethodTurnStart, TurnStartParams{ThreadID: thread.Thread.ID, Input: []UserInput{TextInput("hi")}}, &turn)
	msgs := client.notificationsUntil(NotifyTurnCompleted)
	require.NotContains(t, methodsOf(msgs), NotifyThreadStarted)
	require.NotContains(t, methodsOf(msgs), NotifyThreadStatusChanged)
	require.Equal(t, NotifyTurnStarted, msgs[0].Method)
}

func TestUsageDelta(t *testing.T) {
	before := TokenUsageBreakdown{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
	now := TokenUsageBreakdown{InputTokens: 30, CachedInputTokens: 4, OutputTokens: 8, TotalTokens: 38}
	require.Equal(t, TokenUsageBreakdown{InputTokens: 20, CachedInputTokens: 4, OutputTokens: 3, TotalTokens: 23}, usageDelta(now, before))
	// Compaction resets the cumulative count: the turn's usage is the total.
	reset := TokenUsageBreakdown{InputTokens: 3, OutputTokens: 1, TotalTokens: 4}
	require.Equal(t, reset, usageDelta(reset, now))
}

func TestPreviewName(t *testing.T) {
	require.Equal(t, "Fix the build", previewName("  Fix   the build\n\nmore details"))
	long := "word word word word word word word word word word word word word word word"
	name := previewName(long)
	require.LessOrEqual(t, len([]rune(name)), 60)
	require.True(t, len(name) > 0)
}
