package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const harnessFixtureTimeout = 5 * time.Second

type harnessFixture struct {
	conn   net.Conn
	reader *LLMHarnessJSONLReader
	writer *LLMHarnessJSONLWriter
}

func newHarnessFixture(t *testing.T) (io.ReadWriteCloser, *harnessFixture) {
	t.Helper()
	client, server := net.Pipe()
	return client, &harnessFixture{conn: server, reader: NewLLMHarnessJSONLReader(server, 0), writer: NewLLMHarnessJSONLWriter(server, 0)}
}

func (f *harnessFixture) read() (map[string]any, error) {
	var frame map[string]any
	if err := f.reader.Decode(&frame); err != nil {
		return nil, err
	}
	return frame, nil
}

func (f *harnessFixture) write(frame any) error { return f.writer.Encode(frame) }

func runFixture(t *testing.T, fn func() error) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	return done
}

func awaitFixture(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(harnessFixtureTimeout):
		t.Fatal("fixture timed out")
	}
}

func frameID(frame map[string]any) int64 { return int64(frame["id"].(float64)) }

func textHarnessInput(daggerID, vendorID, text string) LLMHarnessInput {
	return LLMHarnessInput{DaggerMessageID: daggerID, VendorMessageID: vendorID, Content: []*LLMMessage{{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: text}}}}}
}

func receiveHarnessEvent(t *testing.T, events <-chan LLMHarnessEvent) LLMHarnessEvent {
	t.Helper()
	select {
	case event, ok := <-events:
		require.True(t, ok, "event stream closed")
		return event
	case <-time.After(harnessFixtureTimeout):
		t.Fatal("timed out waiting for harness event")
		return nil
	}
}

func closeHarnessAdapter(t *testing.T, adapter LLMHarnessAdapter) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), harnessFixtureTimeout)
	defer cancel()
	require.NoError(t, adapter.Close(ctx))
}

func TestLLMHarnessCommandSpecs(t *testing.T) {
	assert.Equal(t, LLMHarnessCommandSpec{Path: "codex", Args: []string{"app-server"}}, CodexLLMHarnessCommand())
	assert.Equal(t, LLMHarnessCommandSpec{Path: "claude", Args: []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}}, ClaudeLLMHarnessCommand())
	assert.Equal(t, []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--resume", "session-1"}, ClaudeLLMHarnessCommand("session-1").Args)
}

func TestCodexHarnessExternalAuthAndRefresh(t *testing.T) {
	transport, fixture := newHarnessFixture(t)
	adapter := NewCodexLLMHarnessAdapter(transport)
	var forces []bool
	auth := &LLMHarnessAuth{Kind: LLMHarnessAuthOAuth, Resolve: func(_ context.Context, force bool) (LLMHarnessAuthState, error) {
		forces = append(forces, force)
		if force {
			return LLMHarnessAuthState{Token: "token-v2", AccountID: "account-v2", PlanType: "business"}, nil
		}
		return LLMHarnessAuthState{Token: "token-v1", AccountID: "account-v1", PlanType: "pro"}, nil
	}}
	fixtureDone := runFixture(t, func() error {
		initialize, err := fixture.read()
		if err != nil {
			return err
		}
		capabilities := initialize["params"].(map[string]any)["capabilities"].(map[string]any)
		if capabilities["experimentalApi"] != true {
			return fmt.Errorf("experimental API capability not enabled: %#v", capabilities)
		}
		if err := fixture.write(map[string]any{"method": "initialize", "id": frameID(initialize), "response": map[string]any{}}); err != nil {
			return err
		}

		login, err := fixture.read()
		if err != nil {
			return err
		}
		if login["method"] != "account/login/start" {
			return fmt.Errorf("got auth method %v", login["method"])
		}
		params := login["params"].(map[string]any)
		if params["type"] != "chatgptAuthTokens" || params["accessToken"] != "token-v1" || params["chatgptAccountId"] != "account-v1" || params["chatgptPlanType"] != "pro" {
			return fmt.Errorf("unexpected login params %#v", params)
		}
		if err := fixture.write(map[string]any{"method": "account/login/start", "id": frameID(login), "response": map[string]any{}}); err != nil {
			return err
		}

		thread, err := fixture.read()
		if err != nil {
			return err
		}
		if err := fixture.write(map[string]any{"method": "thread/start", "id": frameID(thread), "response": map[string]any{"thread": map[string]any{"id": "thread-auth"}}}); err != nil {
			return err
		}

		if err := fixture.write(map[string]any{
			"method": "account/chatgptAuthTokens/refresh",
			"id":     99,
			"params": map[string]any{"reason": "unauthorized", "previousAccountId": "account-v1"},
		}); err != nil {
			return err
		}
		refresh, err := fixture.read()
		if err != nil {
			return err
		}
		result := refresh["result"].(map[string]any)
		if refresh["id"] != float64(99) || result["accessToken"] != "token-v2" || result["chatgptAccountId"] != "account-v2" || result["chatgptPlanType"] != "business" {
			return fmt.Errorf("unexpected refresh response %#v", refresh)
		}
		return nil
	})

	session, err := adapter.Start(context.Background(), LLMHarnessStart{Auth: auth})
	require.NoError(t, err)
	assert.Equal(t, "thread-auth", session.NativeSession)
	awaitFixture(t, fixtureDone)
	assert.Equal(t, []bool{false, true}, forces)
	closeHarnessAdapter(t, adapter)
}

func TestCodexHarnessAPIKeyLogin(t *testing.T) {
	transport, fixture := newHarnessFixture(t)
	adapter := NewCodexLLMHarnessAdapter(transport)
	auth := &LLMHarnessAuth{Kind: LLMHarnessAuthAPIKey, Resolve: func(_ context.Context, force bool) (LLMHarnessAuthState, error) {
		if force {
			return LLMHarnessAuthState{}, fmt.Errorf("API key must not be refreshed")
		}
		return LLMHarnessAuthState{Token: "openai-api-key"}, nil
	}}
	fixtureDone := runFixture(t, func() error {
		initialize, err := fixture.read()
		if err != nil {
			return err
		}
		if err := fixture.write(map[string]any{"method": "initialize", "id": frameID(initialize), "response": map[string]any{}}); err != nil {
			return err
		}
		login, err := fixture.read()
		if err != nil {
			return err
		}
		params := login["params"].(map[string]any)
		if login["method"] != "account/login/start" || params["type"] != "apiKey" || params["apiKey"] != "openai-api-key" {
			return fmt.Errorf("unexpected API-key login %#v", login)
		}
		if err := fixture.write(map[string]any{"method": "account/login/start", "id": frameID(login), "response": map[string]any{}}); err != nil {
			return err
		}
		thread, err := fixture.read()
		if err != nil {
			return err
		}
		return fixture.write(map[string]any{"method": "thread/start", "id": frameID(thread), "response": map[string]any{"thread": map[string]any{"id": "thread-api-key"}}})
	})

	session, err := adapter.Start(context.Background(), LLMHarnessStart{Auth: auth})
	require.NoError(t, err)
	assert.Equal(t, "thread-api-key", session.NativeSession)
	awaitFixture(t, fixtureDone)
	closeHarnessAdapter(t, adapter)
}

func TestCodexHarnessCommandsAndDefinitiveConsumption(t *testing.T) {
	transport, fixture := newHarnessFixture(t)
	adapter := NewCodexLLMHarnessAdapter(transport)
	allowLifecycle := make(chan struct{})
	fixtureDone := runFixture(t, func() error {
		initialize, err := fixture.read()
		if err != nil {
			return err
		}
		if initialize["method"] != "initialize" {
			return fmt.Errorf("got method %v", initialize["method"])
		}
		if err := fixture.write(map[string]any{"method": "initialize", "id": frameID(initialize), "response": map[string]any{"userAgent": "fixture"}}); err != nil {
			return err
		}

		thread, err := fixture.read()
		if err != nil {
			return err
		}
		if thread["method"] != "thread/start" {
			return fmt.Errorf("got method %v", thread["method"])
		}
		params := thread["params"].(map[string]any)
		if params["model"] != "gpt-fixture" {
			return fmt.Errorf("got model %v", params["model"])
		}
		if err := fixture.write(map[string]any{"method": "thread/start", "id": frameID(thread), "response": map[string]any{"thread": map[string]any{"id": "thread-1"}}}); err != nil {
			return err
		}

		turn, err := fixture.read()
		if err != nil {
			return err
		}
		if turn["method"] != "turn/start" {
			return fmt.Errorf("got method %v", turn["method"])
		}
		turnParams := turn["params"].(map[string]any)
		if turnParams["clientUserMessageId"] != "opaque-message-1" {
			return fmt.Errorf("got client id %v", turnParams["clientUserMessageId"])
		}
		input := turnParams["input"].([]any)[0].(map[string]any)
		if input["text"] != "hello" {
			return fmt.Errorf("got text %v", input["text"])
		}
		if err := fixture.write(map[string]any{"method": "turn/start", "id": frameID(turn), "response": map[string]any{"turn": map[string]any{"id": "turn-1"}}}); err != nil {
			return err
		}

		<-allowLifecycle
		if err := fixture.write(map[string]any{"method": "turn/started", "params": map[string]any{"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "inProgress"}}}); err != nil {
			return err
		}
		if err := fixture.write(map[string]any{"method": "item/started", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"type": "userMessage", "id": "user-1", "clientId": "opaque-message-1"}}}); err != nil {
			return err
		}
		if err := fixture.write(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "itemId": "assistant-1", "delta": "partial"}}); err != nil {
			return err
		}
		if err := fixture.write(map[string]any{"method": "item/started", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"type": "commandExecution", "id": "shell-1", "command": "printf ok"}}}); err != nil {
			return err
		}
		if err := fixture.write(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"type": "commandExecution", "id": "shell-1", "aggregatedOutput": "ok", "exitCode": 0}}}); err != nil {
			return err
		}
		if err := fixture.write(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "completed"}}}); err != nil {
			return err
		}
		return nil
	})

	session, err := adapter.Start(context.Background(), LLMHarnessStart{Model: "gpt-fixture"})
	require.NoError(t, err)
	assert.Equal(t, "thread-1", session.NativeSession)
	require.NoError(t, adapter.StartTurn(context.Background(), textHarnessInput("opaque-message-1", "opaque-message-1", "hello")))

	select {
	case event := <-adapter.Events():
		t.Fatalf("RPC acceptance was incorrectly treated as consumption: %#v", event)
	case <-time.After(25 * time.Millisecond):
	}
	close(allowLifecycle)
	assert.Equal(t, LLMHarnessTurn{NativeTurnID: "turn-1", State: LLMHarnessTurnStarted}, receiveHarnessEvent(t, adapter.Events()))
	assert.Equal(t, LLMHarnessMessageLifecycle{DaggerMessageID: "opaque-message-1", VendorMessageID: "opaque-message-1", NativeTurn: "turn-1", State: LLMHarnessMessageStarted}, receiveHarnessEvent(t, adapter.Events()))
	assert.Equal(t, LLMHarnessTextDelta{Block: 1, Delta: "partial"}, receiveHarnessEvent(t, adapter.Events()))
	assert.Equal(t, LLMHarnessToolCall{Block: 2, CallID: "shell-1", Name: "shell", Arguments: JSON(`{"command":"printf ok"}`), Source: LLMHarnessToolSourceNative}, receiveHarnessEvent(t, adapter.Events()))
	assert.Equal(t, LLMHarnessToolResult{CallID: "shell-1", Text: "ok"}, receiveHarnessEvent(t, adapter.Events()))
	assert.Equal(t, LLMHarnessTurn{NativeTurnID: "turn-1", State: LLMHarnessTurnCompleted}, receiveHarnessEvent(t, adapter.Events()))
	assert.Equal(t, LLMHarnessCompleted{NativeTurnID: "turn-1"}, receiveHarnessEvent(t, adapter.Events()))
	awaitFixture(t, fixtureDone)
	closeHarnessAdapter(t, adapter)
}

func startCodexFixture(t *testing.T) (*CodexLLMHarnessAdapter, *harnessFixture) {
	t.Helper()
	transport, fixture := newHarnessFixture(t)
	adapter := NewCodexLLMHarnessAdapter(transport)
	done := runFixture(t, func() error {
		initialize, err := fixture.read()
		if err != nil {
			return err
		}
		if err := fixture.write(map[string]any{"method": "initialize", "id": frameID(initialize), "response": map[string]any{}}); err != nil {
			return err
		}
		thread, err := fixture.read()
		if err != nil {
			return err
		}
		return fixture.write(map[string]any{"method": "thread/start", "id": frameID(thread), "response": map[string]any{"thread": map[string]any{"id": "thread-correlation"}}})
	})
	_, err := adapter.Start(context.Background(), LLMHarnessStart{})
	require.NoError(t, err)
	awaitFixture(t, done)
	return adapter, fixture
}

func TestCodexHarnessTurnMismatchAndInterrupt(t *testing.T) {
	adapter, fixture := startCodexFixture(t)
	fixtureDone := runFixture(t, func() error {
		steer, err := fixture.read()
		if err != nil {
			return err
		}
		if steer["method"] != "turn/steer" {
			return fmt.Errorf("got %v", steer["method"])
		}
		params := steer["params"].(map[string]any)
		if params["expectedTurnId"] != "turn-stale" {
			return fmt.Errorf("got expected turn %v", params["expectedTurnId"])
		}
		if err := fixture.write(map[string]any{"method": "turn/steer", "id": frameID(steer), "error": map[string]any{"code": -32602, "message": "expectedTurnId does not match active turn"}}); err != nil {
			return err
		}
		interrupt, err := fixture.read()
		if err != nil {
			return err
		}
		if interrupt["method"] != "turn/interrupt" {
			return fmt.Errorf("got %v", interrupt["method"])
		}
		return fixture.write(map[string]any{"method": "turn/interrupt", "id": frameID(interrupt), "response": map[string]any{}})
	})
	err := adapter.Steer(context.Background(), "turn-stale", textHarnessInput("message-2", "message-2", "steer"))
	require.ErrorIs(t, err, ErrCodexTurnMismatch)
	require.NoError(t, adapter.Interrupt(context.Background(), "turn-active", false))
	awaitFixture(t, fixtureDone)
	closeHarnessAdapter(t, adapter)
}

func TestCodexHarnessCorrelatesOutOfOrderResponses(t *testing.T) {
	adapter, fixture := startCodexFixture(t)
	fixtureDone := runFixture(t, func() error {
		first, err := fixture.read()
		if err != nil {
			return err
		}
		second, err := fixture.read()
		if err != nil {
			return err
		}
		byMethod := map[string]map[string]any{first["method"].(string): first, second["method"].(string): second}
		if byMethod["turn/steer"] == nil || byMethod["turn/interrupt"] == nil {
			return fmt.Errorf("unexpected methods %v and %v", first["method"], second["method"])
		}
		interrupt := byMethod["turn/interrupt"]
		if err := fixture.write(map[string]any{"method": "turn/interrupt", "id": frameID(interrupt), "response": map[string]any{}}); err != nil {
			return err
		}
		steer := byMethod["turn/steer"]
		return fixture.write(map[string]any{"method": "turn/steer", "id": frameID(steer), "response": map[string]any{"turnId": "turn-1"}})
	})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs <- adapter.Steer(context.Background(), "turn-1", textHarnessInput("message-3", "message-3", "more"))
	}()
	go func() { defer wg.Done(); errs <- adapter.Interrupt(context.Background(), "turn-1", false) }()
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	awaitFixture(t, fixtureDone)
	closeHarnessAdapter(t, adapter)
}

func TestCodexHarnessMalformedAndEOF(t *testing.T) {
	for _, tc := range []struct {
		name        string
		breakStream func(*harnessFixture) error
		want        error
	}{
		{name: "malformed", breakStream: func(f *harnessFixture) error { _, err := f.conn.Write([]byte("not-json\n")); return err }, want: ErrLLMHarnessMalformedJSONL},
		{name: "EOF", breakStream: func(f *harnessFixture) error { return f.conn.Close() }, want: io.EOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport, fixture := newHarnessFixture(t)
			adapter := NewCodexLLMHarnessAdapter(transport)
			done := runFixture(t, func() error {
				initialize, err := fixture.read()
				if err != nil {
					return err
				}
				if err := fixture.write(map[string]any{"method": "initialize", "id": frameID(initialize), "response": map[string]any{}}); err != nil {
					return err
				}
				_, err = fixture.read()
				if err != nil {
					return err
				}
				return tc.breakStream(fixture)
			})
			_, err := adapter.Start(context.Background(), LLMHarnessStart{})
			require.ErrorIs(t, err, ErrLLMHarnessProtocolFailure)
			require.ErrorIs(t, err, tc.want)
			awaitFixture(t, done)
		})
	}
}

func TestCodexHarnessResumesCheckpointThread(t *testing.T) {
	transport, fixture := newHarnessFixture(t)
	adapter := NewCodexLLMHarnessAdapter(transport)
	fixtureDone := runFixture(t, func() error {
		initialize, err := fixture.read()
		if err != nil {
			return err
		}
		if err := fixture.write(map[string]any{"method": "initialize", "id": frameID(initialize), "response": map[string]any{}}); err != nil {
			return err
		}
		resume, err := fixture.read()
		if err != nil {
			return err
		}
		if resume["method"] != "thread/resume" {
			return fmt.Errorf("got %v", resume["method"])
		}
		if resume["params"].(map[string]any)["threadId"] != "checkpoint-thread" {
			return fmt.Errorf("got resume params %#v", resume["params"])
		}
		return fixture.write(map[string]any{"method": "thread/resume", "id": frameID(resume), "response": map[string]any{"thread": map[string]any{"id": "checkpoint-thread"}}})
	})
	checkpoint := &LLMHarnessCheckpoint{NativeSession: "checkpoint-thread", Protocol: codexHarnessProtocol}
	session, err := adapter.Start(context.Background(), LLMHarnessStart{Checkpoint: checkpoint})
	require.NoError(t, err)
	assert.Equal(t, "checkpoint-thread", session.NativeSession)
	awaitFixture(t, fixtureDone)
	closeHarnessAdapter(t, adapter)
}

func TestClaudeHarnessLifecyclePartialResultAndControls(t *testing.T) {
	transport, fixture := newHarnessFixture(t)
	adapter := NewClaudeLLMHarnessAdapter(transport)
	vendorID := uuid.NewString()
	lifecycle := make(chan struct{})
	fixtureDone := runFixture(t, func() error {
		if err := fixture.write(map[string]any{"type": "system", "subtype": "init", "session_id": "claude-session", "capabilities": []string{"interrupt_receipt_v1", "interrupt_cancel_queued_v1", "cancel_async_message_v1"}}); err != nil {
			return err
		}
		user, err := fixture.read()
		if err != nil {
			return err
		}
		if user["type"] != "user" || user["uuid"] != vendorID {
			return fmt.Errorf("unexpected user frame %#v", user)
		}
		message := user["message"].(map[string]any)
		content := message["content"].([]any)[0].(map[string]any)
		if content["text"] != "hello Claude" {
			return fmt.Errorf("got content %v", content["text"])
		}
		<-lifecycle
		if err := fixture.write(map[string]any{"type": "system", "subtype": "command_lifecycle", "uuid": vendorID, "command_id": "command-1", "state": "started"}); err != nil {
			return err
		}
		if err := fixture.write(map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "partial Claude"}}}); err != nil {
			return err
		}
		if err := fixture.write(map[string]any{"type": "result", "subtype": "interrupted", "command_id": "command-1", "is_error": true}); err != nil {
			return err
		}

		interrupt, err := fixture.read()
		if err != nil {
			return err
		}
		if interrupt["type"] != "control_request" {
			return fmt.Errorf("unexpected control %#v", interrupt)
		}
		request := interrupt["request"].(map[string]any)
		if request["subtype"] != "interrupt" || request["command_id"] != "command-1" {
			return fmt.Errorf("unexpected interrupt %#v", request)
		}
		if err := fixture.write(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": interrupt["request_id"], "still_queued": []string{vendorID}}}); err != nil {
			return err
		}
		cancelAll, err := fixture.read()
		if err != nil {
			return err
		}
		if cancelAll["request"].(map[string]any)["subtype"] != "cancel_queued" {
			return fmt.Errorf("unexpected cancel queued %#v", cancelAll)
		}
		if err := fixture.write(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": cancelAll["request_id"]}}); err != nil {
			return err
		}
		cancelOne, err := fixture.read()
		if err != nil {
			return err
		}
		cancelRequest := cancelOne["request"].(map[string]any)
		if cancelRequest["subtype"] != "cancel_async_message" || cancelRequest["uuid"] != vendorID {
			return fmt.Errorf("unexpected async cancel %#v", cancelRequest)
		}
		return fixture.write(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": cancelOne["request_id"]}})
	})

	session, err := adapter.Start(context.Background(), LLMHarnessStart{})
	require.NoError(t, err)
	assert.Equal(t, "claude-session", session.NativeSession)
	require.NoError(t, adapter.StartTurn(context.Background(), textHarnessInput("dagger-message-1", vendorID, "hello Claude")))
	select {
	case event := <-adapter.Events():
		t.Fatalf("stdin write was incorrectly treated as consumption: %#v", event)
	case <-time.After(25 * time.Millisecond):
	}
	close(lifecycle)
	assert.Equal(t, LLMHarnessMessageLifecycle{DaggerMessageID: "dagger-message-1", VendorMessageID: vendorID, NativeTurn: "command-1", State: LLMHarnessMessageStarted}, receiveHarnessEvent(t, adapter.Events()))
	assert.Equal(t, LLMHarnessTurn{NativeTurnID: "command-1", State: LLMHarnessTurnStarted}, receiveHarnessEvent(t, adapter.Events()))
	assert.Equal(t, LLMHarnessTextDelta{Block: 1, Delta: "partial Claude"}, receiveHarnessEvent(t, adapter.Events()))
	assert.Equal(t, LLMHarnessTurn{NativeTurnID: "command-1", State: LLMHarnessTurnInterrupted}, receiveHarnessEvent(t, adapter.Events()))
	assert.Equal(t, LLMHarnessInterrupted{NativeTurnID: "command-1"}, receiveHarnessEvent(t, adapter.Events()))
	require.NoError(t, adapter.Interrupt(context.Background(), "command-1", true))
	require.NoError(t, adapter.CancelQueued(context.Background(), "dagger-message-1"))
	awaitFixture(t, fixtureDone)
	closeHarnessAdapter(t, adapter)
}

func TestClaudeHarnessCompletedResult(t *testing.T) {
	transport, fixture := newHarnessFixture(t)
	adapter := NewClaudeLLMHarnessAdapter(transport)
	fixtureDone := runFixture(t, func() error {
		if err := fixture.write(map[string]any{"type": "system", "subtype": "init", "session_id": "claude-complete"}); err != nil {
			return err
		}
		return fixture.write(map[string]any{"type": "result", "subtype": "success", "command_id": "command-complete", "usage": map[string]any{"input_tokens": 2, "output_tokens": 3}})
	})
	_, err := adapter.Start(context.Background(), LLMHarnessStart{})
	require.NoError(t, err)
	assert.Equal(t, LLMHarnessUsage{Usage: LLMTokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}}, receiveHarnessEvent(t, adapter.Events()))
	assert.Equal(t, LLMHarnessTurn{NativeTurnID: "command-complete", State: LLMHarnessTurnCompleted}, receiveHarnessEvent(t, adapter.Events()))
	assert.Equal(t, LLMHarnessCompleted{NativeTurnID: "command-complete"}, receiveHarnessEvent(t, adapter.Events()))
	awaitFixture(t, fixtureDone)
	closeHarnessAdapter(t, adapter)
}

func TestClaudeHarnessMalformedFrame(t *testing.T) {
	transport, fixture := newHarnessFixture(t)
	adapter := NewClaudeLLMHarnessAdapter(transport)
	done := runFixture(t, func() error { _, err := fixture.conn.Write([]byte("{broken\n")); return err })
	_, err := adapter.Start(context.Background(), LLMHarnessStart{})
	require.ErrorIs(t, err, ErrLLMHarnessProtocolFailure)
	require.ErrorIs(t, err, ErrLLMHarnessMalformedJSONL)
	awaitFixture(t, done)
}

func TestClaudeHarnessRejectsUnknownQueuedReceipt(t *testing.T) {
	transport, fixture := newHarnessFixture(t)
	adapter := NewClaudeLLMHarnessAdapter(transport)
	done := runFixture(t, func() error {
		if err := fixture.write(map[string]any{"type": "system", "subtype": "init", "session_id": "claude-bad-receipt", "capabilities": []string{"interrupt_receipt_v1"}}); err != nil {
			return err
		}
		control, err := fixture.read()
		if err != nil {
			return err
		}
		return fixture.write(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": control["request_id"], "still_queued": []string{uuid.NewString()}}})
	})
	_, err := adapter.Start(context.Background(), LLMHarnessStart{})
	require.NoError(t, err)
	err = adapter.Interrupt(context.Background(), "command", false)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrLLMHarnessUnknownCorrelation) || errors.Is(err, ErrLLMHarnessProtocolFailure), "unexpected error: %v", err)
	awaitFixture(t, done)
}
