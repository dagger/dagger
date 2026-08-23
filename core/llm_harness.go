package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/opencontainers/go-digest"
)

// LLMHarnessAdapter is the common control protocol implemented by a live CLI
// harness. Process and container ownership deliberately live outside this
// interface.
type LLMHarnessAdapter interface {
	Start(context.Context, LLMHarnessStart) (LLMHarnessSession, error)
	StartTurn(context.Context, LLMHarnessInput) error
	Steer(context.Context, string, LLMHarnessInput) error
	Interrupt(context.Context, string, bool) error
	CancelQueued(context.Context, string) error
	Events() <-chan LLMHarnessEvent
	Quiesce(context.Context) (LLMHarnessNativeState, error)
	Close(context.Context) error
}

// LLMHarnessStart is the portable input needed to start or restore an adapter.
type LLMHarnessStart struct {
	History    []*LLMMessage
	Checkpoint *LLMHarnessCheckpoint
	Model      string
	MaxTokens  int
	MCPURL     string
	MCPToken   string
	CallDigest string
}

// LLMHarnessInput is one canonical Dagger mailbox record translated for a
// vendor protocol.
type LLMHarnessInput struct {
	DaggerMessageID string
	VendorMessageID string
	Content         []*LLMMessage
}

// LLMHarnessSession describes the negotiated hot native session. Its values
// are opaque to the common protocol layer.
type LLMHarnessSession struct {
	NativeSession string   `json:"native_session"`
	Protocol      string   `json:"protocol"`
	Capabilities  []string `json:"capabilities,omitempty"`
}

// LLMHarnessNativeState is opaque state returned only at a quiescent native
// boundary. Its fields map directly to the persisted checkpoint shape.
type LLMHarnessNativeState struct {
	NativeSession string
	Protocol      string
}

func llmHarnessHistoryDigest(messages []*LLMMessage) (digest.Digest, error) {
	encoded, err := json.Marshal(messages)
	if err != nil {
		return "", fmt.Errorf("marshal LLM harness history: %w", err)
	}
	return digest.FromBytes(encoded), nil
}

func (checkpoint *LLMHarnessCheckpoint) validFor(messages []*LLMMessage, kind LLMHarnessKind) bool {
	if checkpoint == nil || checkpoint.Kind != kind || checkpoint.MessageCount < 0 || checkpoint.MessageCount > len(messages) {
		return false
	}
	historyDigest, err := llmHarnessHistoryDigest(messages[:checkpoint.MessageCount])
	return err == nil && historyDigest == checkpoint.HistoryDigest
}

// LLMHarnessTurnState is a native turn's typed lifecycle state.
type LLMHarnessTurnState string

const (
	LLMHarnessTurnStarted     LLMHarnessTurnState = "started"
	LLMHarnessTurnCompleted   LLMHarnessTurnState = "completed"
	LLMHarnessTurnInterrupted LLMHarnessTurnState = "interrupted"
	LLMHarnessTurnFailed      LLMHarnessTurnState = "failed"
)

// LLMHarnessMessageState is definitive or provisional lifecycle evidence for
// one correlated native input.
type LLMHarnessMessageState string

const (
	LLMHarnessMessageQueued    LLMHarnessMessageState = "queued"
	LLMHarnessMessageStarted   LLMHarnessMessageState = "started"
	LLMHarnessMessageCompleted LLMHarnessMessageState = "completed"
	LLMHarnessMessageCancelled LLMHarnessMessageState = "cancelled"
	LLMHarnessMessageDiscarded LLMHarnessMessageState = "discarded"
	LLMHarnessMessageRefused   LLMHarnessMessageState = "refused"
)

// LLMHarnessToolSource distinguishes native CLI tools from Dagger MCP calls.
type LLMHarnessToolSource string

const (
	LLMHarnessToolSourceNative LLMHarnessToolSource = "native"
	LLMHarnessToolSourceMCP    LLMHarnessToolSource = "mcp"
)

// LLMHarnessEvent is the closed common vocabulary produced by adapters.
type LLMHarnessEvent interface {
	isLLMHarnessEvent()
}

// LLMHarnessTurn reports a native turn lifecycle transition.
type LLMHarnessTurn struct {
	NativeTurnID string
	State        LLMHarnessTurnState
}

// LLMHarnessMessageLifecycle reports lifecycle evidence after resolving both
// identifiers through the correlation ledger.
type LLMHarnessMessageLifecycle struct {
	DaggerMessageID string
	VendorMessageID string
	NativeTurn      string
	State           LLMHarnessMessageState
}

// LLMHarnessTextDelta is an incremental assistant text block.
type LLMHarnessTextDelta struct {
	Block int64
	Delta string
}

// LLMHarnessThinkingDelta is an incremental assistant reasoning block.
type LLMHarnessThinkingDelta struct {
	Block     int64
	Delta     string
	Signature string
}

// LLMHarnessToolCall is an observable native or MCP tool invocation.
type LLMHarnessToolCall struct {
	Block     int64
	CallID    string
	Name      string
	Arguments JSON
	Source    LLMHarnessToolSource
}

// LLMHarnessToolResult is the observable result of a tool invocation.
type LLMHarnessToolResult struct {
	CallID string
	Text   string
	Error  bool
}

// LLMHarnessUsage carries native token accounting.
type LLMHarnessUsage struct {
	Usage LLMTokenUsage
}

// LLMHarnessCompleted marks successful terminal turn evidence.
type LLMHarnessCompleted struct {
	NativeTurnID string
}

// LLMHarnessInterrupted marks interrupted terminal turn evidence.
type LLMHarnessInterrupted struct {
	NativeTurnID string
}

func (LLMHarnessTurn) isLLMHarnessEvent()             {}
func (LLMHarnessMessageLifecycle) isLLMHarnessEvent() {}
func (LLMHarnessTextDelta) isLLMHarnessEvent()        {}
func (LLMHarnessThinkingDelta) isLLMHarnessEvent()    {}
func (LLMHarnessToolCall) isLLMHarnessEvent()         {}
func (LLMHarnessToolResult) isLLMHarnessEvent()       {}
func (LLMHarnessUsage) isLLMHarnessEvent()            {}
func (LLMHarnessCompleted) isLLMHarnessEvent()        {}
func (LLMHarnessInterrupted) isLLMHarnessEvent()      {}

var (
	ErrLLMHarnessInvalidCorrelation   = errors.New("invalid LLM harness message correlation")
	ErrLLMHarnessDuplicateCorrelation = errors.New("duplicate LLM harness message correlation")
	ErrLLMHarnessCorrelationConflict  = errors.New("conflicting LLM harness message correlation")
	ErrLLMHarnessUnknownCorrelation   = errors.New("unknown LLM harness message correlation")
	// ErrLLMHarnessExpectedTurn classifies a steer rejected because the
	// observed native turn ended before acceptance. The dispatcher retries the
	// same canonical FIFO head as a new turn.
	ErrLLMHarnessExpectedTurn = errors.New("LLM harness expected turn mismatch")
)

// LLMHarnessCorrelationError identifies the correlation record which failed.
type LLMHarnessCorrelationError struct {
	DaggerMessageID string
	VendorMessageID string
	Err             error
}

func (e *LLMHarnessCorrelationError) Error() string {
	return fmt.Sprintf("llm harness correlation dagger=%q vendor=%q: %v", e.DaggerMessageID, e.VendorMessageID, e.Err)
}

func (e *LLMHarnessCorrelationError) Unwrap() error {
	return e.Err
}

// LLMHarnessCorrelationLedger is a concurrency-safe, insertion-ordered,
// bidirectional correlation ledger. Correlations returns checkpoint-ready FIFO
// state, and NewLLMHarnessCorrelationLedger validates that state on restore.
type LLMHarnessCorrelationLedger struct {
	kind LLMHarnessKind

	mu       sync.RWMutex
	ordered  []LLMHarnessMessageCorrelation
	byDagger map[string]string
	byVendor map[string]string
}

// NewLLMHarnessCorrelationLedger restores and validates a correlation ledger.
// Passing nil correlations creates an empty ledger.
func NewLLMHarnessCorrelationLedger(kind LLMHarnessKind, correlations []LLMHarnessMessageCorrelation) (*LLMHarnessCorrelationLedger, error) {
	if kind != LLMHarnessCodex && kind != LLMHarnessClaude {
		return nil, fmt.Errorf("unsupported LLM harness kind %q", kind)
	}
	ledger := &LLMHarnessCorrelationLedger{
		kind:     kind,
		ordered:  make([]LLMHarnessMessageCorrelation, 0, len(correlations)),
		byDagger: make(map[string]string, len(correlations)),
		byVendor: make(map[string]string, len(correlations)),
	}
	for _, correlation := range correlations {
		if err := ledger.recordLocked(correlation); err != nil {
			return nil, err
		}
	}
	return ledger, nil
}

// Correlate returns the stable vendor ID for a Dagger message, allocating it
// exactly once when necessary. Codex carries the Dagger ID unchanged; Claude
// receives a distinct RFC 4122 UUID.
func (l *LLMHarnessCorrelationLedger) Correlate(daggerMessageID string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if vendorMessageID, ok := l.byDagger[daggerMessageID]; ok {
		return vendorMessageID, nil
	}
	if daggerMessageID == "" {
		return "", correlationError(daggerMessageID, "", ErrLLMHarnessInvalidCorrelation)
	}

	vendorMessageID := daggerMessageID
	if l.kind == LLMHarnessClaude {
		for {
			vendorMessageID = uuid.NewString()
			if vendorMessageID != daggerMessageID {
				if _, exists := l.byVendor[vendorMessageID]; !exists {
					break
				}
			}
		}
	}
	correlation := LLMHarnessMessageCorrelation{
		DaggerMessageID: daggerMessageID,
		VendorMessageID: vendorMessageID,
	}
	if err := l.recordLocked(correlation); err != nil {
		return "", err
	}
	return vendorMessageID, nil
}

// Record adds an externally supplied correlation, applying the same strict
// validation used when restoring checkpoint state.
func (l *LLMHarnessCorrelationLedger) Record(correlation LLMHarnessMessageCorrelation) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recordLocked(correlation)
}

// VendorMessageID resolves a Dagger message ID.
func (l *LLMHarnessCorrelationLedger) VendorMessageID(daggerMessageID string) (string, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	vendorMessageID, ok := l.byDagger[daggerMessageID]
	if !ok {
		return "", correlationError(daggerMessageID, "", ErrLLMHarnessUnknownCorrelation)
	}
	return vendorMessageID, nil
}

// DaggerMessageID reverse-resolves a vendor lifecycle ID.
func (l *LLMHarnessCorrelationLedger) DaggerMessageID(vendorMessageID string) (string, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	daggerMessageID, ok := l.byVendor[vendorMessageID]
	if !ok {
		return "", correlationError("", vendorMessageID, ErrLLMHarnessUnknownCorrelation)
	}
	return daggerMessageID, nil
}

// Correlations returns a defensive FIFO copy suitable for a checkpoint.
func (l *LLMHarnessCorrelationLedger) Correlations() []LLMHarnessMessageCorrelation {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]LLMHarnessMessageCorrelation(nil), l.ordered...)
}

func (l *LLMHarnessCorrelationLedger) recordLocked(correlation LLMHarnessMessageCorrelation) error {
	if err := l.validate(correlation); err != nil {
		return err
	}
	if vendorMessageID, exists := l.byDagger[correlation.DaggerMessageID]; exists {
		err := ErrLLMHarnessCorrelationConflict
		if vendorMessageID == correlation.VendorMessageID {
			err = ErrLLMHarnessDuplicateCorrelation
		}
		return correlationError(correlation.DaggerMessageID, correlation.VendorMessageID, err)
	}
	if daggerMessageID, exists := l.byVendor[correlation.VendorMessageID]; exists {
		err := ErrLLMHarnessCorrelationConflict
		if daggerMessageID == correlation.DaggerMessageID {
			err = ErrLLMHarnessDuplicateCorrelation
		}
		return correlationError(correlation.DaggerMessageID, correlation.VendorMessageID, err)
	}
	l.byDagger[correlation.DaggerMessageID] = correlation.VendorMessageID
	l.byVendor[correlation.VendorMessageID] = correlation.DaggerMessageID
	l.ordered = append(l.ordered, correlation)
	return nil
}

func (l *LLMHarnessCorrelationLedger) validate(correlation LLMHarnessMessageCorrelation) error {
	if correlation.DaggerMessageID == "" || correlation.VendorMessageID == "" {
		return correlationError(correlation.DaggerMessageID, correlation.VendorMessageID, ErrLLMHarnessInvalidCorrelation)
	}
	switch l.kind {
	case LLMHarnessCodex:
		if correlation.VendorMessageID != correlation.DaggerMessageID {
			return correlationError(correlation.DaggerMessageID, correlation.VendorMessageID, ErrLLMHarnessInvalidCorrelation)
		}
	case LLMHarnessClaude:
		vendorUUID, err := uuid.Parse(correlation.VendorMessageID)
		if err != nil || vendorUUID.Variant() != uuid.RFC4122 || correlation.VendorMessageID == correlation.DaggerMessageID {
			return correlationError(correlation.DaggerMessageID, correlation.VendorMessageID, ErrLLMHarnessInvalidCorrelation)
		}
	default:
		return fmt.Errorf("unsupported LLM harness kind %q", l.kind)
	}
	return nil
}

func correlationError(daggerMessageID, vendorMessageID string, err error) error {
	return &LLMHarnessCorrelationError{
		DaggerMessageID: daggerMessageID,
		VendorMessageID: vendorMessageID,
		Err:             err,
	}
}
