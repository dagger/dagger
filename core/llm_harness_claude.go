package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"

	"github.com/google/uuid"
)

const claudeHarnessProtocol = "claude-stream-json-v1"

var ErrClaudeControlUnsupported = errors.New("Claude control operation is not supported")

// ClaudeLLMHarnessCommand returns the persistent Claude stream-JSON command.
// Passing a native session ID appends the CLI resume flags for cold restore.
func ClaudeLLMHarnessCommand(nativeSessionID ...string) LLMHarnessCommandSpec {
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}
	if len(nativeSessionID) > 0 && nativeSessionID[0] != "" {
		args = append(args, "--resume", nativeSessionID[0])
	}
	return LLMHarnessCommandSpec{Path: "claude", Args: args}
}

type claudeInit struct {
	sessionID    string
	capabilities []string
	err          error
}

type claudeControlResult struct {
	response json.RawMessage
	err      error
}

// ClaudeLLMHarnessAdapter speaks the bidirectional stream-JSON protocol over a
// single persistent Claude process. User and control writes are serialized;
// output is decoded concurrently into typed harness events.
type ClaudeLLMHarnessAdapter struct {
	transport io.ReadWriteCloser
	reader    *LLMHarnessJSONLReader
	writer    *LLMHarnessJSONLWriter

	writeMu sync.Mutex
	mu      sync.Mutex
	started bool
	closing bool
	fatal   error

	sessionID    string
	capabilities []string
	ledger       *LLMHarnessCorrelationLedger
	currentTurn  string

	controls  map[string]chan claudeControlResult
	init      chan claudeInit
	events    chan LLMHarnessEvent
	stop      chan struct{}
	done      chan struct{}
	stopOnce  sync.Once
	closeOnce sync.Once
}

func NewClaudeLLMHarnessAdapter(transport io.ReadWriteCloser) *ClaudeLLMHarnessAdapter {
	return &ClaudeLLMHarnessAdapter{
		transport: transport,
		reader:    NewLLMHarnessJSONLReader(transport, 0),
		writer:    NewLLMHarnessJSONLWriter(transport, 0),
		controls:  map[string]chan claudeControlResult{},
		init:      make(chan claudeInit, 1),
		events:    make(chan LLMHarnessEvent, 64),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

func (a *ClaudeLLMHarnessAdapter) Start(ctx context.Context, start LLMHarnessStart) (LLMHarnessSession, error) {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return LLMHarnessSession{}, fmt.Errorf("%w: Claude adapter already started", ErrLLMHarnessProtocolFailure)
	}
	a.started = true
	var correlations []LLMHarnessMessageCorrelation
	if start.Checkpoint != nil {
		correlations = start.Checkpoint.Correlations
	}
	ledger, err := NewLLMHarnessCorrelationLedger(LLMHarnessClaude, correlations)
	if err != nil {
		a.mu.Unlock()
		return LLMHarnessSession{}, err
	}
	a.ledger = ledger
	a.mu.Unlock()

	go a.readLoop()
	select {
	case initialized := <-a.init:
		if initialized.err != nil {
			return LLMHarnessSession{}, initialized.err
		}
		if initialized.sessionID == "" {
			return LLMHarnessSession{}, fmt.Errorf("%w: Claude init omitted session_id", ErrLLMHarnessProtocolFailure)
		}
		if start.Checkpoint != nil && start.Checkpoint.Protocol == claudeHarnessProtocol {
			expected := start.Checkpoint.NativeSession
			if expected != "" && expected != initialized.sessionID {
				return LLMHarnessSession{}, fmt.Errorf("%w: Claude resumed session %q, expected %q", ErrLLMHarnessProtocolFailure, initialized.sessionID, expected)
			}
		}
		a.mu.Lock()
		a.sessionID = initialized.sessionID
		a.capabilities = append([]string(nil), initialized.capabilities...)
		a.mu.Unlock()
		return LLMHarnessSession{NativeSession: initialized.sessionID, Protocol: claudeHarnessProtocol, Capabilities: initialized.capabilities}, nil
	case <-ctx.Done():
		return LLMHarnessSession{}, ctx.Err()
	case <-a.done:
		a.mu.Lock()
		err := a.fatal
		a.mu.Unlock()
		if err == nil {
			err = io.EOF
		}
		return LLMHarnessSession{}, err
	}
}

func (a *ClaudeLLMHarnessAdapter) StartTurn(ctx context.Context, input LLMHarnessInput) error {
	return a.writeUser(ctx, input)
}

func (a *ClaudeLLMHarnessAdapter) Steer(ctx context.Context, _ string, input LLMHarnessInput) error {
	return a.writeUser(ctx, input)
}

func (a *ClaudeLLMHarnessAdapter) Interrupt(ctx context.Context, nativeTurnID string, cancelQueued bool) error {
	request := map[string]any{"subtype": "interrupt"}
	if nativeTurnID != "" {
		request["command_id"] = nativeTurnID
	}
	if _, err := a.control(ctx, request); err != nil {
		return err
	}
	if !cancelQueued {
		return nil
	}
	if !a.hasCapability("interrupt_cancel_queued_v1") {
		return fmt.Errorf("%w: interrupt_cancel_queued_v1", ErrClaudeControlUnsupported)
	}
	_, err := a.control(ctx, map[string]any{"subtype": "cancel_queued"})
	return err
}

func (a *ClaudeLLMHarnessAdapter) CancelQueued(ctx context.Context, messageID string) error {
	if !a.hasCapability("cancel_async_message_v1") && !a.hasCapability("interrupt_cancel_queued_v1") {
		return fmt.Errorf("%w: cancel_async_message", ErrClaudeControlUnsupported)
	}
	a.mu.Lock()
	ledger := a.ledger
	a.mu.Unlock()
	if ledger == nil {
		return fmt.Errorf("%w: Claude adapter is not started", ErrLLMHarnessProtocolFailure)
	}
	vendorID, err := ledger.VendorMessageID(messageID)
	if err != nil {
		if _, reverseErr := ledger.DaggerMessageID(messageID); reverseErr == nil {
			vendorID = messageID
		} else {
			return err
		}
	}
	_, err = a.control(ctx, map[string]any{"subtype": "cancel_async_message", "uuid": vendorID})
	return err
}

func (a *ClaudeLLMHarnessAdapter) Events() <-chan LLMHarnessEvent { return a.events }

func (a *ClaudeLLMHarnessAdapter) Quiesce(context.Context) (LLMHarnessNativeState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fatal != nil {
		return LLMHarnessNativeState{}, a.fatal
	}
	if a.sessionID == "" {
		return LLMHarnessNativeState{}, fmt.Errorf("%w: Claude session is not initialized", ErrLLMHarnessProtocolFailure)
	}
	return LLMHarnessNativeState{NativeSession: a.sessionID, Protocol: claudeHarnessProtocol}, nil
}

func (a *ClaudeLLMHarnessAdapter) Close(ctx context.Context) error {
	a.mu.Lock()
	a.closing = true
	a.mu.Unlock()
	a.stopOnce.Do(func() { close(a.stop) })
	err := a.transport.Close()
	select {
	case <-a.done:
	case <-ctx.Done():
		return errors.Join(err, ctx.Err())
	}
	return err
}

func (a *ClaudeLLMHarnessAdapter) writeUser(ctx context.Context, input LLMHarnessInput) error {
	if err := a.registerInput(input); err != nil {
		return err
	}
	a.mu.Lock()
	if a.fatal != nil {
		err := a.fatal
		a.mu.Unlock()
		return err
	}
	sessionID := a.sessionID
	a.mu.Unlock()
	if sessionID == "" {
		return fmt.Errorf("%w: Claude adapter is not started", ErrLLMHarnessProtocolFailure)
	}
	frame := map[string]any{
		"type":               "user",
		"uuid":               input.VendorMessageID,
		"session_id":         sessionID,
		"parent_tool_use_id": nil,
		"message": map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "text", "text": llmHarnessMessagesText(input.Content)}},
		},
	}
	return a.writeFrame(ctx, frame)
}

func (a *ClaudeLLMHarnessAdapter) control(ctx context.Context, request map[string]any) (json.RawMessage, error) {
	requestID := uuid.NewString()
	result := make(chan claudeControlResult, 1)
	a.mu.Lock()
	if a.fatal != nil {
		err := a.fatal
		a.mu.Unlock()
		return nil, err
	}
	a.controls[requestID] = result
	a.mu.Unlock()
	if err := a.writeFrame(ctx, map[string]any{"type": "control_request", "request_id": requestID, "request": request}); err != nil {
		a.removeControl(requestID)
		return nil, err
	}
	select {
	case response := <-result:
		return response.response, response.err
	case <-ctx.Done():
		a.removeControl(requestID)
		return nil, ctx.Err()
	case <-a.done:
		a.mu.Lock()
		err := a.fatal
		a.mu.Unlock()
		if err == nil {
			err = io.EOF
		}
		return nil, err
	}
}

func (a *ClaudeLLMHarnessAdapter) writeFrame(ctx context.Context, frame any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	a.writeMu.Lock()
	err := a.writer.Encode(frame)
	a.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("write Claude stream frame: %w", err)
	}
	return nil
}

func (a *ClaudeLLMHarnessAdapter) readLoop() {
	defer a.closeOnce.Do(func() { close(a.done); close(a.events) })
	for {
		record, err := a.reader.ReadRecord()
		if err != nil {
			a.failProtocol(err)
			return
		}
		var header struct{ Type, Subtype string }
		if err := json.Unmarshal(record, &header); err != nil {
			a.failProtocol(fmt.Errorf("%w: decode Claude frame: %v", ErrLLMHarnessProtocolFailure, err))
			return
		}
		if header.Type == "" {
			a.failProtocol(fmt.Errorf("%w: Claude frame omitted type", ErrLLMHarnessProtocolFailure))
			return
		}
		if header.Type == "system" && header.Subtype == "init" {
			var initFrame struct {
				SessionID    string   `json:"session_id"`
				Capabilities []string `json:"capabilities"`
			}
			if err := json.Unmarshal(record, &initFrame); err != nil {
				a.failProtocol(err)
				return
			}
			select {
			case a.init <- claudeInit{sessionID: initFrame.SessionID, capabilities: initFrame.Capabilities}:
			default:
				a.failProtocol(fmt.Errorf("%w: duplicate Claude init", ErrLLMHarnessProtocolFailure))
				return
			}
			continue
		}
		if header.Type == "control_response" {
			if err := a.deliverControl(record); err != nil {
				a.failProtocol(err)
				return
			}
			continue
		}
		events, err := a.claudeEvents(header.Type, header.Subtype, record)
		if err != nil {
			a.failProtocol(err)
			return
		}
		for _, event := range events {
			select {
			case a.events <- event:
			case <-a.stop:
				return
			}
		}
	}
}

func (a *ClaudeLLMHarnessAdapter) deliverControl(record json.RawMessage) error {
	var frame struct {
		RequestID string `json:"request_id"`
		Response  struct {
			RequestID   string          `json:"request_id"`
			Subtype     string          `json:"subtype"`
			Error       string          `json:"error"`
			StillQueued []string        `json:"still_queued"`
			Raw         json.RawMessage `json:"-"`
		} `json:"response"`
	}
	if err := json.Unmarshal(record, &frame); err != nil {
		return fmt.Errorf("%w: decode Claude control response: %v", ErrLLMHarnessProtocolFailure, err)
	}
	requestID := frame.RequestID
	if requestID == "" {
		requestID = frame.Response.RequestID
	}
	if requestID == "" {
		return fmt.Errorf("%w: Claude control response omitted request_id", ErrLLMHarnessProtocolFailure)
	}
	a.mu.Lock()
	pending, ok := a.controls[requestID]
	ledger := a.ledger
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: Claude control response has unknown request_id %q", ErrLLMHarnessProtocolFailure, requestID)
	}
	seen := map[string]struct{}{}
	for _, vendorID := range frame.Response.StillQueued {
		if _, duplicate := seen[vendorID]; duplicate {
			return fmt.Errorf("%w: duplicate still_queued UUID %q", ErrLLMHarnessCorrelationConflict, vendorID)
		}
		seen[vendorID] = struct{}{}
		if ledger == nil {
			return fmt.Errorf("%w: Claude correlation ledger missing", ErrLLMHarnessProtocolFailure)
		}
		if _, err := ledger.DaggerMessageID(vendorID); err != nil {
			return err
		}
	}
	var responseEnvelope struct {
		Response json.RawMessage `json:"response"`
	}
	_ = json.Unmarshal(record, &responseEnvelope)
	result := claudeControlResult{response: responseEnvelope.Response}
	if frame.Response.Subtype == "error" || frame.Response.Subtype == "refused" || frame.Response.Error != "" {
		result.err = fmt.Errorf("Claude control %s: %s", frame.Response.Subtype, frame.Response.Error)
	}
	a.mu.Lock()
	delete(a.controls, requestID)
	a.mu.Unlock()
	pending <- result
	return nil
}

func (a *ClaudeLLMHarnessAdapter) claudeEvents(frameType, subtype string, record json.RawMessage) ([]LLMHarnessEvent, error) {
	switch frameType {
	case "system":
		if subtype == "command_lifecycle" {
			return a.claudeLifecycle(record)
		}
		return nil, nil
	case "command_lifecycle":
		return a.claudeLifecycle(record)
	case "assistant":
		return a.claudeAssistant(record)
	case "stream_event":
		return a.claudePartial(record)
	case "result":
		return a.claudeResult(subtype, record)
	default:
		return nil, nil
	}
}

func (a *ClaudeLLMHarnessAdapter) claudeLifecycle(record json.RawMessage) ([]LLMHarnessEvent, error) {
	var frame struct {
		UUID      string `json:"uuid"`
		CommandID string `json:"command_id"`
		State     string `json:"state"`
		Status    string `json:"status"`
		Event     string `json:"event"`
		Lifecycle *struct {
			UUID      string `json:"uuid"`
			CommandID string `json:"command_id"`
			State     string `json:"state"`
		} `json:"command_lifecycle"`
	}
	if err := json.Unmarshal(record, &frame); err != nil {
		return nil, fmt.Errorf("%w: decode Claude command_lifecycle: %v", ErrLLMHarnessProtocolFailure, err)
	}
	if frame.Lifecycle != nil {
		if frame.UUID == "" {
			frame.UUID = frame.Lifecycle.UUID
		}
		if frame.CommandID == "" {
			frame.CommandID = frame.Lifecycle.CommandID
		}
		if frame.State == "" {
			frame.State = frame.Lifecycle.State
		}
	}
	stateName := frame.State
	if stateName == "" {
		stateName = frame.Status
	}
	if stateName == "" {
		stateName = frame.Event
	}
	state, ok := map[string]LLMHarnessMessageState{
		"queued": LLMHarnessMessageQueued, "started": LLMHarnessMessageStarted,
		"completed": LLMHarnessMessageCompleted, "cancelled": LLMHarnessMessageCancelled,
		"discarded": LLMHarnessMessageDiscarded, "refused": LLMHarnessMessageRefused,
	}[stateName]
	if !ok {
		return nil, fmt.Errorf("%w: unknown Claude command_lifecycle state %q", ErrLLMHarnessProtocolFailure, stateName)
	}
	a.mu.Lock()
	ledger := a.ledger
	if frame.CommandID != "" && state == LLMHarnessMessageStarted {
		a.currentTurn = frame.CommandID
	}
	a.mu.Unlock()
	if ledger == nil {
		return nil, fmt.Errorf("%w: Claude correlation ledger missing", ErrLLMHarnessProtocolFailure)
	}
	daggerID, err := ledger.DaggerMessageID(frame.UUID)
	if err != nil {
		return nil, err
	}
	events := []LLMHarnessEvent{LLMHarnessMessageLifecycle{DaggerMessageID: daggerID, VendorMessageID: frame.UUID, NativeTurn: frame.CommandID, State: state}}
	if state == LLMHarnessMessageStarted && frame.CommandID != "" {
		events = append(events, LLMHarnessTurn{NativeTurnID: frame.CommandID, State: LLMHarnessTurnStarted})
	}
	return events, nil
}

func (a *ClaudeLLMHarnessAdapter) claudeAssistant(record json.RawMessage) ([]LLMHarnessEvent, error) {
	var frame struct {
		Message struct {
			Content []struct {
				Type, Text, Thinking, Signature, ID, Name string
				Input                                     json.RawMessage `json:"input"`
			} `json:"content"`
			Usage struct {
				InputTokens, OutputTokens, CacheReadInputTokens, CacheCreationInputTokens int64 `json:"-"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(record, &frame); err != nil {
		return nil, fmt.Errorf("%w: decode Claude assistant: %v", ErrLLMHarnessProtocolFailure, err)
	}
	// Decode usage separately because the wire uses snake_case.
	var usageEnvelope struct {
		Message struct {
			Usage struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
				CacheRead    int64 `json:"cache_read_input_tokens"`
				CacheWrite   int64 `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	_ = json.Unmarshal(record, &usageEnvelope)
	var events []LLMHarnessEvent
	for index, content := range frame.Message.Content {
		block := int64(index + 1)
		switch content.Type {
		case "text":
			events = append(events, LLMHarnessTextDelta{Block: block, Delta: content.Text})
		case "thinking":
			events = append(events, LLMHarnessThinkingDelta{Block: block, Delta: content.Thinking, Signature: content.Signature})
		case "tool_use":
			events = append(events, LLMHarnessToolCall{Block: block, CallID: content.ID, Name: content.Name, Arguments: JSON(content.Input), Source: LLMHarnessToolSourceNative})
		}
	}
	u := usageEnvelope.Message.Usage
	if u.InputTokens != 0 || u.OutputTokens != 0 || u.CacheRead != 0 || u.CacheWrite != 0 {
		events = append(events, LLMHarnessUsage{Usage: LLMTokenUsage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedTokenReads: u.CacheRead, CachedTokenWrites: u.CacheWrite, TotalTokens: u.InputTokens + u.OutputTokens + u.CacheRead + u.CacheWrite}})
	}
	return events, nil
}

func (a *ClaudeLLMHarnessAdapter) claudePartial(record json.RawMessage) ([]LLMHarnessEvent, error) {
	var frame struct {
		Event struct {
			Type         string `json:"type"`
			Index        int64  `json:"index"`
			ContentBlock struct {
				Type, ID, Name string
				Input          json.RawMessage `json:"input"`
			} `json:"content_block"`
			Delta struct {
				Type, Text, Thinking, Signature, PartialJSON string `json:"-"`
			} `json:"delta"`
		} `json:"event"`
	}
	if err := json.Unmarshal(record, &frame); err != nil {
		return nil, fmt.Errorf("%w: decode Claude stream_event: %v", ErrLLMHarnessProtocolFailure, err)
	}
	var deltaEnvelope struct {
		Event struct {
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		} `json:"event"`
	}
	_ = json.Unmarshal(record, &deltaEnvelope)
	block := frame.Event.Index + 1
	switch frame.Event.Type {
	case "content_block_start":
		if frame.Event.ContentBlock.Type == "tool_use" {
			return []LLMHarnessEvent{LLMHarnessToolCall{Block: block, CallID: frame.Event.ContentBlock.ID, Name: frame.Event.ContentBlock.Name, Arguments: JSON(frame.Event.ContentBlock.Input), Source: LLMHarnessToolSourceNative}}, nil
		}
	case "content_block_delta":
		delta := deltaEnvelope.Event.Delta
		switch delta.Type {
		case "text_delta":
			return []LLMHarnessEvent{LLMHarnessTextDelta{Block: block, Delta: delta.Text}}, nil
		case "thinking_delta", "signature_delta":
			return []LLMHarnessEvent{LLMHarnessThinkingDelta{Block: block, Delta: delta.Thinking, Signature: delta.Signature}}, nil
		}
	}
	return nil, nil
}

func (a *ClaudeLLMHarnessAdapter) claudeResult(subtype string, record json.RawMessage) ([]LLMHarnessEvent, error) {
	var frame struct {
		Subtype   string `json:"subtype"`
		CommandID string `json:"command_id"`
		IsError   bool   `json:"is_error"`
		Usage     struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
			CacheRead    int64 `json:"cache_read_input_tokens"`
			CacheWrite   int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(record, &frame); err != nil {
		return nil, fmt.Errorf("%w: decode Claude result: %v", ErrLLMHarnessProtocolFailure, err)
	}
	if subtype == "" {
		subtype = frame.Subtype
	}
	a.mu.Lock()
	turnID := frame.CommandID
	if turnID == "" {
		turnID = a.currentTurn
	}
	a.mu.Unlock()
	var events []LLMHarnessEvent
	u := frame.Usage
	if u.InputTokens != 0 || u.OutputTokens != 0 || u.CacheRead != 0 || u.CacheWrite != 0 {
		events = append(events, LLMHarnessUsage{Usage: LLMTokenUsage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedTokenReads: u.CacheRead, CachedTokenWrites: u.CacheWrite, TotalTokens: u.InputTokens + u.OutputTokens + u.CacheRead + u.CacheWrite}})
	}
	switch subtype {
	case "success":
		events = append(events, LLMHarnessTurn{NativeTurnID: turnID, State: LLMHarnessTurnCompleted}, LLMHarnessCompleted{NativeTurnID: turnID})
	case "interrupted", "aborted", "cancelled":
		events = append(events, LLMHarnessTurn{NativeTurnID: turnID, State: LLMHarnessTurnInterrupted}, LLMHarnessInterrupted{NativeTurnID: turnID})
	default:
		if frame.IsError || subtype == "error_during_execution" || subtype == "error_max_turns" {
			events = append(events, LLMHarnessTurn{NativeTurnID: turnID, State: LLMHarnessTurnFailed})
		} else {
			return nil, fmt.Errorf("%w: unknown Claude result subtype %q", ErrLLMHarnessProtocolFailure, subtype)
		}
	}
	return events, nil
}

func (a *ClaudeLLMHarnessAdapter) registerInput(input LLMHarnessInput) error {
	if input.DaggerMessageID == "" || input.VendorMessageID == "" {
		return fmt.Errorf("%w: input correlation is empty", ErrLLMHarnessInvalidCorrelation)
	}
	a.mu.Lock()
	ledger := a.ledger
	a.mu.Unlock()
	if ledger == nil {
		return fmt.Errorf("%w: Claude adapter is not started", ErrLLMHarnessProtocolFailure)
	}
	vendor, err := ledger.VendorMessageID(input.DaggerMessageID)
	if err == nil {
		if vendor != input.VendorMessageID {
			return fmt.Errorf("%w: input correlation changed", ErrLLMHarnessCorrelationConflict)
		}
		return nil
	}
	if !errors.Is(err, ErrLLMHarnessUnknownCorrelation) {
		return err
	}
	return ledger.Record(LLMHarnessMessageCorrelation{DaggerMessageID: input.DaggerMessageID, VendorMessageID: input.VendorMessageID})
}

func (a *ClaudeLLMHarnessAdapter) failProtocol(cause error) {
	a.mu.Lock()
	if a.closing && errors.Is(cause, io.EOF) {
		a.mu.Unlock()
		return
	}
	if a.fatal == nil {
		a.fatal = fmt.Errorf("%w: Claude stream-json: %w", ErrLLMHarnessProtocolFailure, cause)
	}
	err := a.fatal
	controls := a.controls
	a.controls = map[string]chan claudeControlResult{}
	a.mu.Unlock()
	select {
	case a.init <- claudeInit{err: err}:
	default:
	}
	for _, pending := range controls {
		pending <- claudeControlResult{err: err}
	}
}

func (a *ClaudeLLMHarnessAdapter) removeControl(id string) {
	a.mu.Lock()
	delete(a.controls, id)
	a.mu.Unlock()
}
func (a *ClaudeLLMHarnessAdapter) hasCapability(capability string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Contains(a.capabilities, capability)
}

var _ LLMHarnessAdapter = (*ClaudeLLMHarnessAdapter)(nil)
