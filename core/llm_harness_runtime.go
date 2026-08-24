package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// LLMHarnessCommit is the complete, quiescent result of one native turn. A
// caller must materialize Messages, mutable tool/workspace state, NativeState,
// and Correlations as one checkpoint before returning from Commit.
type LLMHarnessCommit struct {
	NativeTurnID     string
	DaggerMessageIDs []string
	Messages         []*LLMMessage
	NativeState      LLMHarnessNativeState
	Correlations     []LLMHarnessMessageCorrelation
	Interrupted      bool
}

// LLMHarnessCommitFunc atomically publishes a terminal native turn. The reply
// returned here is the value delivered to every AgentMessage consumed by the
// turn. A nil function uses the last assistant text from the accumulated
// messages and is useful to synchronous callers and tests.
type LLMHarnessCommitFunc func(context.Context, LLMHarnessCommit) (string, error)

// LLMHarnessRuntime owns exactly one hot adapter. It serializes canonical
// inputs, keeps acknowledged input at the FIFO head until lifecycle proves
// consumption, accumulates native events, and resolves each input against the
// terminal native turn which consumed it.
type LLMHarnessRuntime struct {
	adapter LLMHarnessAdapter
	ledger  *LLMHarnessCorrelationLedger
	commit  LLMHarnessCommitFunc

	ctx    context.Context
	cancel context.CancelCauseFunc
	done   chan struct{}
	wake   chan struct{}

	mu      sync.Mutex
	records map[string]*llmHarnessRuntimeRecord
	queue   []string
	closed  bool
	err     error

	activeTurn string
}

type llmHarnessRuntimeRecord struct {
	input LLMHarnessInput

	// accepted records a successful StartTurn/Steer response. The record remains
	// the FIFO head awaiting lifecycle, but must not be written again meanwhile.
	accepted bool

	delivery      AgentMessageDelivery
	deliveryErr   error
	deliveryReady chan struct{}

	turnID string
	reply  string
	err    error
	ready  chan struct{}
}

type llmHarnessDispatchResult struct {
	record   *llmHarnessRuntimeRecord
	delivery AgentMessageDelivery
	err      error
}

type llmHarnessInterruptRequest struct {
	ctx  context.Context
	done chan error
}

var errLLMHarnessRuntimeClosed = errors.New("LLM harness runtime closed")

type llmHarnessBlock struct {
	index int64
	seq   int
	block *LLMContentBlock
}

type llmHarnessAccumulator struct {
	blocks  []llmHarnessBlock
	byIndex map[int64]int
	results []*LLMMessage
	usage   LLMTokenUsage
	seq     int
}

// NewLLMHarnessRuntime starts one adapter and its single FIFO dispatcher.
func NewLLMHarnessRuntime(ctx context.Context, kind LLMHarnessKind, adapter LLMHarnessAdapter, start LLMHarnessStart, commit LLMHarnessCommitFunc) (*LLMHarnessRuntime, error) {
	if adapter == nil {
		return nil, fmt.Errorf("LLM harness adapter is required")
	}
	var correlations []LLMHarnessMessageCorrelation
	if start.Checkpoint != nil {
		correlations = start.Checkpoint.Correlations
	}
	ledger, err := NewLLMHarnessCorrelationLedger(kind, correlations)
	if err != nil {
		return nil, err
	}
	if _, err := adapter.Start(ctx, start); err != nil {
		return nil, fmt.Errorf("start LLM harness: %w", err)
	}
	runCtx, cancel := context.WithCancelCause(ctx)
	runtime := &LLMHarnessRuntime{
		adapter: adapter,
		ledger:  ledger,
		commit:  commit,
		ctx:     runCtx,
		cancel:  cancel,
		done:    make(chan struct{}),
		wake:    make(chan struct{}, 1),
		records: map[string]*llmHarnessRuntimeRecord{},
	}
	go runtime.run()
	return runtime, nil
}

// Enqueue durably appends one canonical input. Adapter writes happen only in
// the dispatcher goroutine and therefore preserve this order under concurrent
// callers.
func (runtime *LLMHarnessRuntime) Enqueue(ctx context.Context, daggerMessageID string, content []*LLMMessage) error {
	vendorMessageID, err := runtime.ledger.Correlate(daggerMessageID)
	if err != nil {
		return err
	}
	record := &llmHarnessRuntimeRecord{
		input: LLMHarnessInput{
			DaggerMessageID: daggerMessageID,
			VendorMessageID: vendorMessageID,
			Content:         cloneLLMMessages(content),
		},
		deliveryReady: make(chan struct{}),
		ready:         make(chan struct{}),
	}

	runtime.mu.Lock()
	if runtime.closed {
		err := runtime.err
		if err == nil {
			err = errors.New("LLM harness runtime is closed")
		}
		runtime.mu.Unlock()
		return err
	}
	if _, exists := runtime.records[daggerMessageID]; exists {
		runtime.mu.Unlock()
		return fmt.Errorf("LLM harness message %q is already enqueued", daggerMessageID)
	}
	runtime.records[daggerMessageID] = record
	runtime.queue = append(runtime.queue, daggerMessageID)
	runtime.mu.Unlock()
	runtime.poke()
	return nil
}

// Delivery waits for definitive correlated lifecycle evidence. Adapter command
// acknowledgement is deliberately insufficient.
func (runtime *LLMHarnessRuntime) Delivery(ctx context.Context, daggerMessageID string) (AgentMessageDelivery, error) {
	runtime.mu.Lock()
	record := runtime.records[daggerMessageID]
	runtime.mu.Unlock()
	if record == nil {
		return "", fmt.Errorf("unknown LLM harness message %q", daggerMessageID)
	}
	select {
	case <-ctx.Done():
		return "", context.Cause(ctx)
	case <-record.deliveryReady:
		runtime.mu.Lock()
		delivery, err := record.delivery, record.deliveryErr
		runtime.mu.Unlock()
		return delivery, err
	}
}

// Await waits for the terminal turn which consumed the exact correlated input.
func (runtime *LLMHarnessRuntime) Await(ctx context.Context, daggerMessageID string) (string, error) {
	runtime.mu.Lock()
	record := runtime.records[daggerMessageID]
	runtime.mu.Unlock()
	if record == nil {
		return "", fmt.Errorf("unknown LLM harness message %q", daggerMessageID)
	}
	select {
	case <-ctx.Done():
		return "", context.Cause(ctx)
	case <-record.ready:
		runtime.mu.Lock()
		reply, err := record.reply, record.err
		runtime.mu.Unlock()
		return reply, err
	}
}

// Interrupt stops further dispatch first, then asks the adapter to interrupt
// the active turn and cancel native queued input. Lifecycle events remain the
// authority for resolving delivery and await.
func (runtime *LLMHarnessRuntime) Interrupt(ctx context.Context) error {
	runtime.mu.Lock()
	turnID := runtime.activeTurn
	runtime.mu.Unlock()
	if turnID == "" {
		return nil
	}
	return runtime.adapter.Interrupt(ctx, turnID, true)
}

// Close releases the hot adapter and waits for dispatcher cleanup. Pending
// records resolve with the close cause and can never hang.
func (runtime *LLMHarnessRuntime) Close(ctx context.Context) error {
	runtime.cancel(errLLMHarnessRuntimeClosed)
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-runtime.done:
		runtime.mu.Lock()
		err := runtime.err
		runtime.mu.Unlock()
		if errors.Is(err, errLLMHarnessRuntimeClosed) {
			return nil
		}
		return err
	}
}

func (runtime *LLMHarnessRuntime) poke() {
	select {
	case runtime.wake <- struct{}{}:
	default:
	}
}

func (runtime *LLMHarnessRuntime) run() {
	defer close(runtime.done)
	defer func() {
		closeErr := runtime.adapter.Close(context.WithoutCancel(runtime.ctx))
		cause := context.Cause(runtime.ctx)
		if cause == nil {
			cause = closeErr
		} else if closeErr != nil {
			cause = errors.Join(cause, closeErr)
		}
		runtime.fail(cause)
	}()

	events := runtime.adapter.Events()
	var pending *llmHarnessRuntimeRecord
	var dispatching bool
	var dispatchResult chan llmHarnessDispatchResult
	consumedByTurn := map[string][]*llmHarnessRuntimeRecord{}
	accumulators := map[string]*llmHarnessAccumulator{}
	finishedTurns := map[string]struct{}{}

	for {
		if pending == nil {
			runtime.mu.Lock()
			if len(runtime.queue) > 0 {
				pending = runtime.records[runtime.queue[0]]
			}
			runtime.mu.Unlock()
		}
		if pending != nil && !pending.accepted && !dispatching {
			dispatching = true
			dispatchResult = make(chan llmHarnessDispatchResult, 1)
			runtime.mu.Lock()
			activeTurn := runtime.activeTurn
			if activeTurn == "" {
				pending.delivery = AgentMessageStarted
			} else {
				pending.delivery = AgentMessageSteered
			}
			runtime.mu.Unlock()
			go runtime.dispatch(pending, activeTurn, dispatchResult)
		}

		select {
		case <-runtime.ctx.Done():
			return
		case <-runtime.wake:
			// Re-evaluate the canonical queue.
		case result := <-dispatchResult:
			dispatching = false
			dispatchResult = nil
			if result.err != nil {
				runtime.cancel(result.err)
				continue
			}
			// Acceptance is transport state only. Keep pending at the FIFO head
			// until a correlated lifecycle event proves consumption. The final
			// dispatch mode may differ after an expected-turn retry. A lifecycle
			// event may race ahead of this response; never overwrite evidence that
			// has already become definitive.
			runtime.mu.Lock()
			select {
			case <-result.record.deliveryReady:
			default:
				result.record.delivery = result.delivery
			}
			result.record.accepted = true
			runtime.mu.Unlock()
		case event, ok := <-events:
			if !ok {
				runtime.cancel(errors.New("LLM harness event stream closed"))
				continue
			}
			switch event := event.(type) {
			case LLMHarnessTurn:
				switch event.State {
				case LLMHarnessTurnStarted:
					runtime.setActiveTurn(event.NativeTurnID)
				case LLMHarnessTurnCompleted:
					if err := runtime.finishTurn(event.NativeTurnID, false, consumedByTurn, accumulators, finishedTurns); err != nil {
						runtime.cancel(err)
					}
				case LLMHarnessTurnInterrupted:
					if err := runtime.finishTurn(event.NativeTurnID, true, consumedByTurn, accumulators, finishedTurns); err != nil {
						runtime.cancel(err)
					}
				case LLMHarnessTurnFailed:
					runtime.cancel(fmt.Errorf("LLM harness turn %q failed", event.NativeTurnID))
				default:
					runtime.cancel(fmt.Errorf("unknown LLM harness turn state %q", event.State))
				}
			case LLMHarnessMessageLifecycle:
				record, consumed, err := runtime.consumeLifecycle(event, pending)
				if err != nil {
					runtime.cancel(err)
					continue
				}
				if consumed {
					consumedByTurn[event.NativeTurn] = append(consumedByTurn[event.NativeTurn], record)
					pending = nil
				}
			case LLMHarnessTextDelta:
				runtime.accumulator(accumulators).text(event)
			case LLMHarnessThinkingDelta:
				runtime.accumulator(accumulators).thinking(event)
			case LLMHarnessToolCall:
				runtime.accumulator(accumulators).toolCall(event)
			case LLMHarnessToolResult:
				runtime.accumulator(accumulators).toolResult(event)
			case LLMHarnessUsage:
				runtime.accumulator(accumulators).addUsage(event.Usage)
			case LLMHarnessCompleted:
				if err := runtime.finishTurn(event.NativeTurnID, false, consumedByTurn, accumulators, finishedTurns); err != nil {
					runtime.cancel(err)
				}
			case LLMHarnessInterrupted:
				if err := runtime.finishTurn(event.NativeTurnID, true, consumedByTurn, accumulators, finishedTurns); err != nil {
					runtime.cancel(err)
				}
			default:
				runtime.cancel(fmt.Errorf("unknown LLM harness event %T", event))
			}
		}
	}
}

func (runtime *LLMHarnessRuntime) dispatch(record *llmHarnessRuntimeRecord, activeTurn string, result chan<- llmHarnessDispatchResult) {
	delivery := AgentMessageStarted
	var err error
	if activeTurn != "" {
		delivery = AgentMessageSteered
		err = runtime.adapter.Steer(runtime.ctx, activeTurn, record.input)
		if errors.Is(err, ErrLLMHarnessExpectedTurn) {
			// Change the pending classification before issuing StartTurn. A fast
			// lifecycle notification from that command may race its response, but
			// can therefore never freeze the stale STEERED intent.
			delivery = AgentMessageStarted
			runtime.mu.Lock()
			record.delivery = delivery
			runtime.mu.Unlock()
			err = runtime.adapter.StartTurn(runtime.ctx, record.input)
		}
	} else {
		err = runtime.adapter.StartTurn(runtime.ctx, record.input)
	}
	if err != nil {
		err = fmt.Errorf("dispatch LLM harness message %q: %w", record.input.DaggerMessageID, err)
	}
	result <- llmHarnessDispatchResult{record: record, delivery: delivery, err: err}
}

func (runtime *LLMHarnessRuntime) consumeLifecycle(event LLMHarnessMessageLifecycle, pending *llmHarnessRuntimeRecord) (*llmHarnessRuntimeRecord, bool, error) {
	daggerID, err := runtime.ledger.DaggerMessageID(event.VendorMessageID)
	if err != nil {
		return nil, false, err
	}
	if daggerID != event.DaggerMessageID {
		return nil, false, fmt.Errorf("LLM harness lifecycle correlation conflict: dagger=%q vendor=%q maps to %q", event.DaggerMessageID, event.VendorMessageID, daggerID)
	}
	runtime.mu.Lock()
	record := runtime.records[daggerID]
	runtime.mu.Unlock()
	if record == nil {
		return nil, false, fmt.Errorf("LLM harness lifecycle references unknown message %q", daggerID)
	}

	switch event.State {
	case LLMHarnessMessageQueued:
		return record, false, nil // provisional acknowledgement
	case LLMHarnessMessageStarted, LLMHarnessMessageCompleted:
		if pending != record {
			// Codex emits both item/started and item/completed for the same
			// userMessage. The first event is definitive consumption; the
			// completion is a duplicate lifecycle observation, not another FIFO
			// dequeue. A completed event can only be ignored after this exact
			// record was already correlated to this exact native turn.
			runtime.mu.Lock()
			alreadyConsumed := event.State == LLMHarnessMessageCompleted && record.turnID == event.NativeTurn && record.turnID != ""
			runtime.mu.Unlock()
			if alreadyConsumed {
				return record, false, nil
			}
			return nil, false, fmt.Errorf("LLM harness consumed message %q out of FIFO order", daggerID)
		}
		if event.NativeTurn == "" {
			return nil, false, fmt.Errorf("LLM harness consumed message %q without a native turn", daggerID)
		}
		runtime.mu.Lock()
		if len(runtime.queue) == 0 || runtime.queue[0] != daggerID {
			runtime.mu.Unlock()
			return nil, false, fmt.Errorf("LLM harness consumed message %q outside the canonical FIFO head", daggerID)
		}
		runtime.queue = runtime.queue[1:]
		runtime.activeTurn = event.NativeTurn
		record.turnID = event.NativeTurn
		delivery := record.delivery
		if delivery == "" {
			// A lifecycle notification can race ahead of the command response.
			// Native turn identity still makes the classification definitive.
			delivery = AgentMessageStarted
		}
		finalizeHarnessDelivery(record, delivery, nil)
		runtime.mu.Unlock()
		runtime.poke()
		return record, true, nil
	case LLMHarnessMessageCancelled, LLMHarnessMessageDiscarded, LLMHarnessMessageRefused:
		if pending != record {
			return nil, false, fmt.Errorf("LLM harness cancelled message %q out of FIFO order", daggerID)
		}
		err := fmt.Errorf("LLM harness message %q was %s before consumption", daggerID, event.State)
		runtime.mu.Lock()
		if len(runtime.queue) > 0 && runtime.queue[0] == daggerID {
			runtime.queue = runtime.queue[1:]
		}
		finalizeHarnessDelivery(record, "", err)
		resolveHarnessRecord(record, "", err)
		runtime.mu.Unlock()
		runtime.poke()
		return record, false, nil
	default:
		return nil, false, fmt.Errorf("unknown LLM harness message state %q", event.State)
	}
}

func (runtime *LLMHarnessRuntime) finishTurn(turnID string, interrupted bool, consumed map[string][]*llmHarnessRuntimeRecord, accumulators map[string]*llmHarnessAccumulator, finished map[string]struct{}) error {
	if turnID == "" {
		return errors.New("LLM harness terminal event has no native turn ID")
	}
	if _, alreadyFinished := finished[turnID]; alreadyFinished {
		// Adapters intentionally expose both the typed turn lifecycle and the
		// terminal convenience event. They describe one boundary, not two commits.
		return nil
	}
	records := consumed[turnID]
	if len(records) == 0 {
		return fmt.Errorf("LLM harness terminal turn %q consumed no Dagger messages", turnID)
	}
	native, err := runtime.adapter.Quiesce(runtime.ctx)
	if err != nil {
		return fmt.Errorf("quiesce LLM harness turn %q: %w", turnID, err)
	}
	accumulator := accumulators[turnID]
	var messages []*LLMMessage
	if accumulator != nil {
		messages = accumulator.messages()
	}
	commit := LLMHarnessCommit{
		NativeTurnID: turnID,
		Messages:     messages,
		NativeState:  native,
		Correlations: runtime.ledger.Correlations(),
		Interrupted:  interrupted,
	}
	for _, record := range records {
		commit.DaggerMessageIDs = append(commit.DaggerMessageIDs, record.input.DaggerMessageID)
	}
	reply := lastHarnessReply(messages)
	if runtime.commit != nil {
		reply, err = runtime.commit(runtime.ctx, commit)
		if err != nil {
			return fmt.Errorf("commit LLM harness turn %q: %w", turnID, err)
		}
	}
	runtime.mu.Lock()
	for _, record := range records {
		resolveHarnessRecord(record, reply, nil)
	}
	if runtime.activeTurn == turnID {
		runtime.activeTurn = ""
	}
	runtime.mu.Unlock()
	delete(consumed, turnID)
	delete(accumulators, turnID)
	finished[turnID] = struct{}{}
	runtime.poke()
	return nil
}

func (runtime *LLMHarnessRuntime) accumulator(accumulators map[string]*llmHarnessAccumulator) *llmHarnessAccumulator {
	runtime.mu.Lock()
	turnID := runtime.activeTurn
	runtime.mu.Unlock()
	accumulator := accumulators[turnID]
	if accumulator == nil {
		accumulator = &llmHarnessAccumulator{byIndex: map[int64]int{}}
		accumulators[turnID] = accumulator
	}
	return accumulator
}

func (runtime *LLMHarnessRuntime) setActiveTurn(turnID string) {
	if turnID == "" {
		runtime.cancel(errors.New("LLM harness started turn has no native turn ID"))
		return
	}
	runtime.mu.Lock()
	runtime.activeTurn = turnID
	runtime.mu.Unlock()
}

func (runtime *LLMHarnessRuntime) fail(err error) {
	if err == nil {
		err = errors.New("LLM harness runtime stopped")
	}
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return
	}
	runtime.closed = true
	runtime.err = err
	for _, record := range runtime.records {
		finalizeHarnessDelivery(record, "", err)
		resolveHarnessRecord(record, "", err)
	}
	runtime.mu.Unlock()
}

func finalizeHarnessDelivery(record *llmHarnessRuntimeRecord, delivery AgentMessageDelivery, err error) {
	select {
	case <-record.deliveryReady:
		return
	default:
	}
	record.delivery = delivery
	record.deliveryErr = err
	close(record.deliveryReady)
}

func resolveHarnessRecord(record *llmHarnessRuntimeRecord, reply string, err error) {
	select {
	case <-record.ready:
		return
	default:
	}
	record.reply = reply
	record.err = err
	close(record.ready)
}

func cloneLLMMessages(messages []*LLMMessage) []*LLMMessage {
	cloned := make([]*LLMMessage, len(messages))
	for index, message := range messages {
		cloned[index] = message.Clone()
	}
	return cloned
}

func (accumulator *llmHarnessAccumulator) block(index int64, kind LLMContentBlockKind) *LLMContentBlock {
	if position, ok := accumulator.byIndex[index]; ok {
		block := accumulator.blocks[position].block
		if block.Kind == kind {
			return block
		}
	}
	block := &LLMContentBlock{Kind: kind}
	accumulator.byIndex[index] = len(accumulator.blocks)
	accumulator.blocks = append(accumulator.blocks, llmHarnessBlock{index: index, seq: accumulator.seq, block: block})
	accumulator.seq++
	return block
}

func (accumulator *llmHarnessAccumulator) text(event LLMHarnessTextDelta) {
	accumulator.block(event.Block, LLMContentText).Text += event.Delta
}

func (accumulator *llmHarnessAccumulator) thinking(event LLMHarnessThinkingDelta) {
	block := accumulator.block(event.Block, LLMContentThinking)
	block.Text += event.Delta
	if event.Signature != "" {
		block.Signature = event.Signature
	}
}

func (accumulator *llmHarnessAccumulator) toolCall(event LLMHarnessToolCall) {
	block := accumulator.block(event.Block, LLMContentToolCall)
	block.CallID = event.CallID
	block.ToolName = event.Name
	block.Arguments = event.Arguments
}

func (accumulator *llmHarnessAccumulator) toolResult(event LLMHarnessToolResult) {
	accumulator.results = append(accumulator.results, &LLMMessage{
		Role: LLMMessageRoleUser,
		Content: []*LLMContentBlock{{
			Kind:    LLMContentToolResult,
			CallID:  event.CallID,
			Text:    event.Text,
			Errored: event.Error,
		}},
	})
}

func (accumulator *llmHarnessAccumulator) addUsage(usage LLMTokenUsage) {
	accumulator.usage.InputTokens += usage.InputTokens
	accumulator.usage.OutputTokens += usage.OutputTokens
	accumulator.usage.CachedTokenReads += usage.CachedTokenReads
	accumulator.usage.CachedTokenWrites += usage.CachedTokenWrites
	accumulator.usage.TotalTokens += usage.TotalTokens
}

func (accumulator *llmHarnessAccumulator) messages() []*LLMMessage {
	slices.SortStableFunc(accumulator.blocks, func(left, right llmHarnessBlock) int {
		if left.index < right.index {
			return -1
		}
		if left.index > right.index {
			return 1
		}
		return left.seq - right.seq
	})
	blocks := make([]*LLMContentBlock, 0, len(accumulator.blocks))
	for _, accumulated := range accumulator.blocks {
		blocks = append(blocks, accumulated.block.Clone())
	}
	messages := make([]*LLMMessage, 0, 1+len(accumulator.results))
	if len(blocks) > 0 || accumulator.usage.hasTokens() {
		usage := accumulator.usage
		messages = append(messages, &LLMMessage{Role: LLMMessageRoleAssistant, Content: blocks, TokenUsage: &usage})
	}
	messages = append(messages, cloneLLMMessages(accumulator.results)...)
	return messages
}

func lastHarnessReply(messages []*LLMMessage) string {
	var reply string
	for _, message := range messages {
		if message.Role == LLMMessageRoleAssistant && message.TextContent() != "" {
			reply = message.TextContent()
		}
	}
	return reply
}
