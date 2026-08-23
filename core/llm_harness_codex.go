package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

const codexHarnessProtocol = "codex-app-server-v2"

var (
	ErrLLMHarnessProtocolFailure = errors.New("LLM harness protocol failure")
	// ErrCodexTurnMismatch remains vendor-specific for callers which want to
	// diagnose Codex, while wrapping the common retry classification consumed by
	// the harness runtime.
	ErrCodexTurnMismatch = fmt.Errorf("Codex active turn does not match expected turn: %w", ErrLLMHarnessExpectedTurn)
)

// LLMHarnessCommandSpec describes the process a container runner must launch.
// The protocol adapters only own the resulting bidirectional stream.
type LLMHarnessCommandSpec struct {
	Path string
	Args []string
}

// CodexLLMHarnessCommand returns the persistent Codex app-server invocation.
func CodexLLMHarnessCommand() LLMHarnessCommandSpec {
	return LLMHarnessCommandSpec{Path: "codex", Args: []string{"app-server"}}
}

type codexRPCResponse struct {
	method   string
	response json.RawMessage
	err      error
}

type codexPendingRequest struct {
	method string
	result chan codexRPCResponse
}

type codexRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *codexRPCError) Error() string {
	return fmt.Sprintf("Codex app-server error %d: %s", e.Code, e.Message)
}

// CodexLLMHarnessAdapter speaks app-server JSON-RPC over one persistent JSONL
// stream. The caller owns launching the command described above.
type CodexLLMHarnessAdapter struct {
	transport io.ReadWriteCloser
	reader    *LLMHarnessJSONLReader
	writer    *LLMHarnessJSONLWriter

	writeMu sync.Mutex
	mu      sync.Mutex
	started bool
	closing bool
	nextID  int64
	pending map[int64]codexPendingRequest
	fatal   error

	threadID  string
	ledger    *LLMHarnessCorrelationLedger
	blocks    map[string]int64
	nextBlock int64

	events    chan LLMHarnessEvent
	stop      chan struct{}
	done      chan struct{}
	stopOnce  sync.Once
	closeOnce sync.Once
}

func NewCodexLLMHarnessAdapter(transport io.ReadWriteCloser) *CodexLLMHarnessAdapter {
	return &CodexLLMHarnessAdapter{
		transport: transport,
		reader:    NewLLMHarnessJSONLReader(transport, 0),
		writer:    NewLLMHarnessJSONLWriter(transport, 0),
		pending:   map[int64]codexPendingRequest{},
		blocks:    map[string]int64{},
		events:    make(chan LLMHarnessEvent, 64),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

func (a *CodexLLMHarnessAdapter) Start(ctx context.Context, start LLMHarnessStart) (LLMHarnessSession, error) {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return LLMHarnessSession{}, fmt.Errorf("%w: Codex adapter already started", ErrLLMHarnessProtocolFailure)
	}
	a.started = true
	var correlations []LLMHarnessMessageCorrelation
	if start.Checkpoint != nil {
		correlations = start.Checkpoint.Correlations
	}
	ledger, err := NewLLMHarnessCorrelationLedger(LLMHarnessCodex, correlations)
	if err != nil {
		a.mu.Unlock()
		return LLMHarnessSession{}, err
	}
	a.ledger = ledger
	a.mu.Unlock()

	go a.readLoop()

	var initialized struct {
		UserAgent string `json:"userAgent"`
	}
	if err := a.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "dagger",
			"title":   "Dagger LLM harness",
			"version": "1",
		},
		"capabilities": map[string]any{},
	}, &initialized); err != nil {
		return LLMHarnessSession{}, fmt.Errorf("initialize Codex app-server: %w", err)
	}

	checkpointThread := ""
	if start.Checkpoint != nil && start.Checkpoint.Protocol == codexHarnessProtocol {
		checkpointThread = start.Checkpoint.NativeSession
	}
	var threadResult struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if checkpointThread != "" {
		params := map[string]any{"threadId": checkpointThread}
		if start.Model != "" {
			params["model"] = start.Model
		}
		err = a.call(ctx, "thread/resume", params, &threadResult)
	} else {
		params := map[string]any{}
		if start.Model != "" {
			params["model"] = start.Model
		}
		err = a.call(ctx, "thread/start", params, &threadResult)
	}
	if err != nil {
		return LLMHarnessSession{}, fmt.Errorf("start Codex thread: %w", err)
	}
	if threadResult.Thread.ID == "" {
		return LLMHarnessSession{}, fmt.Errorf("%w: Codex thread response omitted thread.id", ErrLLMHarnessProtocolFailure)
	}
	a.mu.Lock()
	a.threadID = threadResult.Thread.ID
	a.mu.Unlock()
	return LLMHarnessSession{
		NativeSession: threadResult.Thread.ID,
		Protocol:      codexHarnessProtocol,
	}, nil
}

func (a *CodexLLMHarnessAdapter) StartTurn(ctx context.Context, input LLMHarnessInput) error {
	if err := a.registerInput(input); err != nil {
		return err
	}
	threadID, err := a.currentThread()
	if err != nil {
		return err
	}
	var result struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	return a.call(ctx, "turn/start", map[string]any{
		"threadId":            threadID,
		"clientUserMessageId": input.VendorMessageID,
		"input":               codexUserInput(input.Content),
	}, &result)
}

func (a *CodexLLMHarnessAdapter) Steer(ctx context.Context, nativeTurnID string, input LLMHarnessInput) error {
	if nativeTurnID == "" {
		return fmt.Errorf("%w: expected turn ID is empty", ErrCodexTurnMismatch)
	}
	if err := a.registerInput(input); err != nil {
		return err
	}
	threadID, err := a.currentThread()
	if err != nil {
		return err
	}
	var result struct {
		TurnID string `json:"turnId"`
	}
	err = a.call(ctx, "turn/steer", map[string]any{
		"threadId":            threadID,
		"clientUserMessageId": input.VendorMessageID,
		"input":               codexUserInput(input.Content),
		"expectedTurnId":      nativeTurnID,
	}, &result)
	if err != nil && codexIsTurnMismatch(err) {
		return fmt.Errorf("%w: %v", ErrCodexTurnMismatch, err)
	}
	return err
}

func (a *CodexLLMHarnessAdapter) Interrupt(ctx context.Context, nativeTurnID string, _ bool) error {
	threadID, err := a.currentThread()
	if err != nil {
		return err
	}
	if nativeTurnID == "" {
		return fmt.Errorf("%w: interrupt turn ID is empty", ErrLLMHarnessProtocolFailure)
	}
	return a.call(ctx, "turn/interrupt", map[string]any{
		"threadId": threadID,
		"turnId":   nativeTurnID,
	}, nil)
}

func (a *CodexLLMHarnessAdapter) CancelQueued(context.Context, string) error {
	return fmt.Errorf("%w: Codex queue cancellation is not negotiated", ErrLLMHarnessProtocolFailure)
}

func (a *CodexLLMHarnessAdapter) Events() <-chan LLMHarnessEvent {
	return a.events
}

func (a *CodexLLMHarnessAdapter) Quiesce(context.Context) (LLMHarnessNativeState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fatal != nil {
		return LLMHarnessNativeState{}, a.fatal
	}
	if a.threadID == "" {
		return LLMHarnessNativeState{}, fmt.Errorf("%w: Codex thread is not initialized", ErrLLMHarnessProtocolFailure)
	}
	return LLMHarnessNativeState{
		NativeSession: a.threadID,
		Protocol:      codexHarnessProtocol,
	}, nil
}

func (a *CodexLLMHarnessAdapter) Close(ctx context.Context) error {
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

func (a *CodexLLMHarnessAdapter) call(ctx context.Context, method string, params any, dst any) error {
	a.mu.Lock()
	if a.fatal != nil {
		err := a.fatal
		a.mu.Unlock()
		return err
	}
	a.nextID++
	id := a.nextID
	result := make(chan codexRPCResponse, 1)
	a.pending[id] = codexPendingRequest{method: method, result: result}
	a.mu.Unlock()

	a.writeMu.Lock()
	err := a.writer.Encode(map[string]any{"method": method, "id": id, "params": params})
	a.writeMu.Unlock()
	if err != nil {
		a.removePending(id)
		return fmt.Errorf("write Codex %s request: %w", method, err)
	}

	select {
	case response := <-result:
		if response.err != nil {
			return response.err
		}
		if response.method != "" && response.method != method {
			return fmt.Errorf("%w: response %d method %q does not match %q", ErrLLMHarnessProtocolFailure, id, response.method, method)
		}
		if dst == nil || len(response.response) == 0 || string(response.response) == "null" {
			return nil
		}
		if err := json.Unmarshal(response.response, dst); err != nil {
			return fmt.Errorf("%w: decode Codex %s response: %v", ErrLLMHarnessProtocolFailure, method, err)
		}
		return nil
	case <-ctx.Done():
		a.removePending(id)
		return ctx.Err()
	case <-a.done:
		a.mu.Lock()
		err := a.fatal
		a.mu.Unlock()
		if err == nil {
			err = io.EOF
		}
		return err
	}
}

func (a *CodexLLMHarnessAdapter) readLoop() {
	defer a.closeOnce.Do(func() {
		close(a.done)
		close(a.events)
	})
	for {
		record, err := a.reader.ReadRecord()
		if err != nil {
			a.failProtocol(err)
			return
		}
		var envelope struct {
			ID       *int64          `json:"id"`
			Method   string          `json:"method"`
			Response json.RawMessage `json:"response"`
			Result   json.RawMessage `json:"result"`
			Error    json.RawMessage `json:"error"`
			Params   json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(record, &envelope); err != nil {
			a.failProtocol(fmt.Errorf("%w: decode Codex frame: %v", ErrLLMHarnessProtocolFailure, err))
			return
		}
		if envelope.ID != nil {
			response := envelope.Response
			if len(response) == 0 {
				response = envelope.Result
			}
			if err := a.deliverResponse(*envelope.ID, envelope.Method, response, envelope.Error); err != nil {
				a.failProtocol(err)
				return
			}
			continue
		}
		if envelope.Method == "" {
			a.failProtocol(fmt.Errorf("%w: Codex frame has neither request id nor method", ErrLLMHarnessProtocolFailure))
			return
		}
		events, err := a.codexEvents(envelope.Method, envelope.Params)
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

func (a *CodexLLMHarnessAdapter) deliverResponse(id int64, method string, response, rawError json.RawMessage) error {
	a.mu.Lock()
	pending, ok := a.pending[id]
	if ok {
		delete(a.pending, id)
	}
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: Codex response has unknown request id %d", ErrLLMHarnessProtocolFailure, id)
	}
	result := codexRPCResponse{method: method, response: response}
	if len(rawError) != 0 && string(rawError) != "null" {
		var rpcErr codexRPCError
		if err := json.Unmarshal(rawError, &rpcErr); err != nil {
			return fmt.Errorf("%w: malformed Codex error response for request %d", ErrLLMHarnessProtocolFailure, id)
		}
		result.err = &rpcErr
	}
	pending.result <- result
	return nil
}

func (a *CodexLLMHarnessAdapter) codexEvents(method string, params json.RawMessage) ([]LLMHarnessEvent, error) {
	switch method {
	case "turn/started":
		var p struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if err := decodeHarnessParams(method, params, &p); err != nil {
			return nil, err
		}
		return []LLMHarnessEvent{LLMHarnessTurn{NativeTurnID: p.Turn.ID, State: LLMHarnessTurnStarted}}, nil
	case "turn/completed":
		var p struct {
			Turn struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"turn"`
		}
		if err := decodeHarnessParams(method, params, &p); err != nil {
			return nil, err
		}
		state := LLMHarnessTurnFailed
		var terminal LLMHarnessEvent
		switch p.Turn.Status {
		case "completed":
			state, terminal = LLMHarnessTurnCompleted, LLMHarnessCompleted{NativeTurnID: p.Turn.ID}
		case "interrupted":
			state, terminal = LLMHarnessTurnInterrupted, LLMHarnessInterrupted{NativeTurnID: p.Turn.ID}
		case "failed":
		default:
			return nil, fmt.Errorf("%w: unknown Codex terminal turn state %q", ErrLLMHarnessProtocolFailure, p.Turn.Status)
		}
		events := []LLMHarnessEvent{LLMHarnessTurn{NativeTurnID: p.Turn.ID, State: state}}
		if terminal != nil {
			events = append(events, terminal)
		}
		return events, nil
	case "item/started", "item/completed":
		return a.codexItemEvent(method, params)
	case "item/agentMessage/delta":
		var p struct {
			ItemID, Delta string
			TurnID        string `json:"turnId"`
		}
		if err := decodeHarnessParams(method, params, &p); err != nil {
			return nil, err
		}
		return []LLMHarnessEvent{LLMHarnessTextDelta{Block: a.block(p.ItemID), Delta: p.Delta}}, nil
	case "item/reasoning/textDelta", "item/reasoning/summaryTextDelta":
		var p struct {
			ItemID, Delta string
			TurnID        string `json:"turnId"`
		}
		if err := decodeHarnessParams(method, params, &p); err != nil {
			return nil, err
		}
		return []LLMHarnessEvent{LLMHarnessThinkingDelta{Block: a.block(p.ItemID), Delta: p.Delta}}, nil
	case "thread/tokenUsage/updated":
		var p struct {
			TokenUsage struct {
				Last struct {
					TotalTokens, InputTokens, CachedInputTokens, CacheWriteInputTokens, OutputTokens int64
				} `json:"last"`
			} `json:"tokenUsage"`
		}
		if err := decodeHarnessParams(method, params, &p); err != nil {
			return nil, err
		}
		u := p.TokenUsage.Last
		return []LLMHarnessEvent{LLMHarnessUsage{Usage: LLMTokenUsage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedTokenReads: u.CachedInputTokens, CachedTokenWrites: u.CacheWriteInputTokens, TotalTokens: u.TotalTokens}}}, nil
	default:
		// App-server has many informational notifications. Unknown notifications
		// are forward-compatible unless they claim mandatory lifecycle semantics.
		return nil, nil
	}
}

func (a *CodexLLMHarnessAdapter) codexItemEvent(method string, params json.RawMessage) ([]LLMHarnessEvent, error) {
	var p struct {
		ThreadID string          `json:"threadId"`
		TurnID   string          `json:"turnId"`
		Item     json.RawMessage `json:"item"`
	}
	if err := decodeHarnessParams(method, params, &p); err != nil {
		return nil, err
	}
	var item struct {
		Type             string          `json:"type"`
		ID               string          `json:"id"`
		ClientID         string          `json:"clientId"`
		Command          string          `json:"command"`
		AggregatedOutput string          `json:"aggregatedOutput"`
		ExitCode         *int            `json:"exitCode"`
		Status           string          `json:"status"`
		Server           string          `json:"server"`
		Tool             string          `json:"tool"`
		Arguments        json.RawMessage `json:"arguments"`
		Result           json.RawMessage `json:"result"`
		Error            json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(p.Item, &item); err != nil {
		return nil, fmt.Errorf("%w: decode Codex %s item: %v", ErrLLMHarnessProtocolFailure, method, err)
	}
	started := method == "item/started"
	switch item.Type {
	case "userMessage":
		if item.ClientID == "" {
			return nil, fmt.Errorf("%w: Codex userMessage omitted clientId", ErrLLMHarnessProtocolFailure)
		}
		daggerID, err := a.ledger.DaggerMessageID(item.ClientID)
		if err != nil {
			return nil, err
		}
		state := LLMHarnessMessageCompleted
		if started {
			state = LLMHarnessMessageStarted
		}
		return []LLMHarnessEvent{LLMHarnessMessageLifecycle{DaggerMessageID: daggerID, VendorMessageID: item.ClientID, NativeTurn: p.TurnID, State: state}}, nil
	case "commandExecution":
		if started {
			args, _ := json.Marshal(map[string]string{"command": item.Command})
			return []LLMHarnessEvent{LLMHarnessToolCall{Block: a.block(item.ID), CallID: item.ID, Name: "shell", Arguments: JSON(args), Source: LLMHarnessToolSourceNative}}, nil
		}
		errored := item.ExitCode != nil && *item.ExitCode != 0
		return []LLMHarnessEvent{LLMHarnessToolResult{CallID: item.ID, Text: item.AggregatedOutput, Error: errored}}, nil
	case "mcpToolCall":
		if started {
			return []LLMHarnessEvent{LLMHarnessToolCall{Block: a.block(item.ID), CallID: item.ID, Name: item.Tool, Arguments: JSON(item.Arguments), Source: LLMHarnessToolSourceMCP}}, nil
		}
		text := string(item.Result)
		errored := len(item.Error) != 0 && string(item.Error) != "null"
		if errored {
			text = string(item.Error)
		}
		return []LLMHarnessEvent{LLMHarnessToolResult{CallID: item.ID, Text: text, Error: errored}}, nil
	default:
		return nil, nil
	}
}

func (a *CodexLLMHarnessAdapter) failProtocol(cause error) {
	a.mu.Lock()
	if a.closing && errors.Is(cause, io.EOF) {
		a.mu.Unlock()
		return
	}
	if a.fatal == nil {
		a.fatal = fmt.Errorf("%w: Codex app-server: %w", ErrLLMHarnessProtocolFailure, cause)
	}
	err := a.fatal
	pending := a.pending
	a.pending = map[int64]codexPendingRequest{}
	a.mu.Unlock()
	for _, request := range pending {
		request.result <- codexRPCResponse{err: err}
	}
}

func (a *CodexLLMHarnessAdapter) currentThread() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fatal != nil {
		return "", a.fatal
	}
	if a.threadID == "" {
		return "", fmt.Errorf("%w: Codex adapter is not started", ErrLLMHarnessProtocolFailure)
	}
	return a.threadID, nil
}

func (a *CodexLLMHarnessAdapter) registerInput(input LLMHarnessInput) error {
	if input.DaggerMessageID == "" || input.VendorMessageID == "" {
		return fmt.Errorf("%w: input correlation is empty", ErrLLMHarnessInvalidCorrelation)
	}
	a.mu.Lock()
	ledger := a.ledger
	a.mu.Unlock()
	if ledger == nil {
		return fmt.Errorf("%w: Codex adapter is not started", ErrLLMHarnessProtocolFailure)
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

func (a *CodexLLMHarnessAdapter) removePending(id int64) {
	a.mu.Lock()
	delete(a.pending, id)
	a.mu.Unlock()
}

func (a *CodexLLMHarnessAdapter) block(itemID string) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if block, ok := a.blocks[itemID]; ok {
		return block
	}
	a.nextBlock++
	a.blocks[itemID] = a.nextBlock
	return a.nextBlock
}

func codexUserInput(messages []*LLMMessage) []map[string]any {
	return []map[string]any{{"type": "text", "text": llmHarnessMessagesText(messages), "text_elements": []any{}}}
}

func llmHarnessMessagesText(messages []*LLMMessage) string {
	var text strings.Builder
	for i, message := range messages {
		if i > 0 {
			text.WriteByte('\n')
		}
		for _, block := range message.Content {
			switch block.Kind {
			case LLMContentText, LLMContentThinking, LLMContentToolResult:
				text.WriteString(block.Text)
			case LLMContentToolCall:
				fmt.Fprintf(&text, "[%s %s]", block.ToolName, block.Arguments)
			}
		}
	}
	return text.String()
}

func codexIsTurnMismatch(err error) bool {
	var rpcErr *codexRPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	message := strings.ToLower(rpcErr.Message + " " + string(rpcErr.Data))
	return strings.Contains(message, "expectedturnid") || strings.Contains(message, "active turn") || strings.Contains(message, "turn mismatch")
}

func decodeHarnessParams(method string, raw json.RawMessage, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return fmt.Errorf("%w: %s notification omitted params", ErrLLMHarnessProtocolFailure, method)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%w: decode %s notification: %v", ErrLLMHarnessProtocolFailure, method, err)
	}
	return nil
}

var _ LLMHarnessAdapter = (*CodexLLMHarnessAdapter)(nil)
