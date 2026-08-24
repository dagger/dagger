package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLLMHarnessCheckpointCursorValidation(t *testing.T) {
	messages := harnessPrompt("one")
	historyDigest, err := llmHarnessHistoryDigest(messages)
	require.NoError(t, err)
	checkpoint := &LLMHarnessCheckpoint{
		Kind:          LLMHarnessCodex,
		MessageCount:  len(messages),
		HistoryDigest: historyDigest,
	}
	assert.True(t, checkpoint.validFor(messages, LLMHarnessCodex))
	assert.False(t, checkpoint.validFor(messages, LLMHarnessClaude))
	assert.True(t, checkpoint.validFor(append(messages, harnessPrompt("suffix")...), LLMHarnessCodex))

	changed := cloneLLMMessages(messages)
	changed[0].Content[0].Text = "changed"
	assert.False(t, checkpoint.validFor(changed, LLMHarnessCodex))

	checkpoint.MessageCount = len(messages) + 1
	assert.False(t, checkpoint.validFor(messages, LLMHarnessCodex))
	assert.False(t, (*LLMHarnessCheckpoint)(nil).validFor(messages, LLMHarnessCodex))
}

func TestCodexHarnessAuthOfferPrefersOAuth(t *testing.T) {
	initial := codexTestToken(t, "account-initial")
	current := codexTestToken(t, "account-current")
	rotated := codexTestToken(t, "account-rotated")
	var forces []bool
	router := &LLMRouter{
		OpenAIAPIKey:         "fallback-api-key",
		OpenAICodexAuthToken: initial,
		reloadCodexAuthToken: func(context.Context) (Credential, error) {
			forces = append(forces, false)
			return Credential{Token: current}, nil
		},
		forceReloadCodexAuthToken: func(context.Context) (Credential, error) {
			forces = append(forces, true)
			return Credential{Token: rotated}, nil
		},
	}

	offer := codexHarnessAuthOffer(router)
	require.NotNil(t, offer)
	assert.Equal(t, LLMHarnessAuthOAuth, offer.Kind)
	state, err := offer.Resolve(t.Context(), false)
	require.NoError(t, err)
	assert.Equal(t, current, state.Token)
	assert.Equal(t, "account-current", state.AccountID)
	state, err = offer.Resolve(t.Context(), true)
	require.NoError(t, err)
	assert.Equal(t, rotated, state.Token)
	assert.Equal(t, "account-rotated", state.AccountID)
	assert.Equal(t, []bool{false, true}, forces)
}

func TestCodexHarnessAuthOfferAPIKeyFallback(t *testing.T) {
	// The offer is selected entirely from Core's router and the explicit CODEX
	// harness kind. There is deliberately no container or marker input to
	// inspect, and the credential never enters the module graph.
	offer := codexHarnessAuthOffer(&LLMRouter{
		OpenAIAPIKey:         "openai-api-key",
		OpenAICodexAuthToken: "not-a-chatgpt-jwt",
	})
	require.NotNil(t, offer)
	assert.Equal(t, LLMHarnessAuthAPIKey, offer.Kind)
	state, err := offer.Resolve(t.Context(), false)
	require.NoError(t, err)
	assert.Equal(t, LLMHarnessAuthState{Token: "openai-api-key"}, state)
}

func TestCodexHarnessAuthOfferNoCredential(t *testing.T) {
	assert.Nil(t, codexHarnessAuthOffer(nil))
	assert.Nil(t, codexHarnessAuthOffer(&LLMRouter{AnthropicAPIKey: "unrelated"}))

	// Non-Codex kinds do not load a router or consult the harness container.
	offer, err := llmHarnessAuthOffer(t.Context(), nil, LLMHarnessClaude)
	require.NoError(t, err)
	assert.Nil(t, offer)
}

func TestLLMHarnessCodexCorrelationFIFO(t *testing.T) {
	ledger, err := NewLLMHarnessCorrelationLedger(LLMHarnessCodex, nil)
	require.NoError(t, err)

	for _, daggerID := range []string{"opaque-3", "opaque-1", "opaque-2"} {
		vendorID, err := ledger.Correlate(daggerID)
		require.NoError(t, err)
		assert.Equal(t, daggerID, vendorID)
	}

	// Allocating an existing record is idempotent and does not move it.
	vendorID, err := ledger.Correlate("opaque-1")
	require.NoError(t, err)
	assert.Equal(t, "opaque-1", vendorID)
	assert.Equal(t, []LLMHarnessMessageCorrelation{
		{DaggerMessageID: "opaque-3", VendorMessageID: "opaque-3"},
		{DaggerMessageID: "opaque-1", VendorMessageID: "opaque-1"},
		{DaggerMessageID: "opaque-2", VendorMessageID: "opaque-2"},
	}, ledger.Correlations())

	for _, id := range []string{"opaque-3", "opaque-1", "opaque-2"} {
		daggerID, err := ledger.DaggerMessageID(id)
		require.NoError(t, err)
		assert.Equal(t, id, daggerID)
	}
}

func TestLLMHarnessClaudeCorrelationCheckpointRestore(t *testing.T) {
	ledger, err := NewLLMHarnessCorrelationLedger(LLMHarnessClaude, nil)
	require.NoError(t, err)

	first, err := ledger.Correlate("opaque-dagger-id-1")
	require.NoError(t, err)
	second, err := ledger.Correlate("opaque-dagger-id-2")
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
	assert.NotEqual(t, "opaque-dagger-id-1", first)
	_, err = uuid.Parse(first)
	require.NoError(t, err)
	_, err = uuid.Parse(second)
	require.NoError(t, err)

	checkpoint := (&LLMHarnessCheckpoint{Correlations: ledger.Correlations()}).clone()
	restored, err := NewLLMHarnessCorrelationLedger(LLMHarnessClaude, checkpoint.Correlations)
	require.NoError(t, err)

	persisted, err := restored.Correlate("opaque-dagger-id-1")
	require.NoError(t, err)
	assert.Equal(t, first, persisted)
	daggerID, err := restored.DaggerMessageID(second)
	require.NoError(t, err)
	assert.Equal(t, "opaque-dagger-id-2", daggerID)
	assert.Equal(t, checkpoint.Correlations, restored.Correlations())

	// Checkpoint input and ledger getters must not expose mutable ledger storage.
	checkpoint.Correlations[0].VendorMessageID = uuid.NewString()
	snapshot := restored.Correlations()
	snapshot[0].VendorMessageID = uuid.NewString()
	persisted, err = restored.VendorMessageID("opaque-dagger-id-1")
	require.NoError(t, err)
	assert.Equal(t, first, persisted)
}

func TestLLMHarnessCorrelationConflicts(t *testing.T) {
	firstUUID := uuid.NewString()
	secondUUID := uuid.NewString()

	t.Run("duplicate record", func(t *testing.T) {
		_, err := NewLLMHarnessCorrelationLedger(LLMHarnessClaude, []LLMHarnessMessageCorrelation{
			{DaggerMessageID: "one", VendorMessageID: firstUUID},
			{DaggerMessageID: "one", VendorMessageID: firstUUID},
		})
		require.ErrorIs(t, err, ErrLLMHarnessDuplicateCorrelation)
	})

	t.Run("dagger ID maps twice", func(t *testing.T) {
		_, err := NewLLMHarnessCorrelationLedger(LLMHarnessClaude, []LLMHarnessMessageCorrelation{
			{DaggerMessageID: "one", VendorMessageID: firstUUID},
			{DaggerMessageID: "one", VendorMessageID: secondUUID},
		})
		require.ErrorIs(t, err, ErrLLMHarnessCorrelationConflict)
	})

	t.Run("vendor ID maps twice", func(t *testing.T) {
		_, err := NewLLMHarnessCorrelationLedger(LLMHarnessClaude, []LLMHarnessMessageCorrelation{
			{DaggerMessageID: "one", VendorMessageID: firstUUID},
			{DaggerMessageID: "two", VendorMessageID: firstUUID},
		})
		require.ErrorIs(t, err, ErrLLMHarnessCorrelationConflict)
	})

	t.Run("invalid Claude UUID", func(t *testing.T) {
		_, err := NewLLMHarnessCorrelationLedger(LLMHarnessClaude, []LLMHarnessMessageCorrelation{
			{DaggerMessageID: "one", VendorMessageID: "not-a-uuid"},
		})
		require.ErrorIs(t, err, ErrLLMHarnessInvalidCorrelation)
	})

	t.Run("Claude UUID must be distinct", func(t *testing.T) {
		daggerUUID := uuid.NewString()
		_, err := NewLLMHarnessCorrelationLedger(LLMHarnessClaude, []LLMHarnessMessageCorrelation{
			{DaggerMessageID: daggerUUID, VendorMessageID: daggerUUID},
		})
		require.ErrorIs(t, err, ErrLLMHarnessInvalidCorrelation)
	})

	t.Run("Codex ID must be unchanged", func(t *testing.T) {
		_, err := NewLLMHarnessCorrelationLedger(LLMHarnessCodex, []LLMHarnessMessageCorrelation{
			{DaggerMessageID: "one", VendorMessageID: "different"},
		})
		require.ErrorIs(t, err, ErrLLMHarnessInvalidCorrelation)
	})

	t.Run("unknown forward and reverse lookup", func(t *testing.T) {
		ledger, err := NewLLMHarnessCorrelationLedger(LLMHarnessClaude, nil)
		require.NoError(t, err)
		_, err = ledger.VendorMessageID("missing")
		require.ErrorIs(t, err, ErrLLMHarnessUnknownCorrelation)
		_, err = ledger.DaggerMessageID(uuid.NewString())
		require.ErrorIs(t, err, ErrLLMHarnessUnknownCorrelation)
	})
}

func TestLLMHarnessJSONLRoundTrip(t *testing.T) {
	var stream bytes.Buffer
	writer := NewLLMHarnessJSONLWriter(&stream, 128)
	require.NoError(t, writer.Encode(map[string]any{"type": "started", "sequence": 1}))
	require.NoError(t, writer.WriteRecord(json.RawMessage(`{"type":"completed"}`)))
	assert.Equal(t, "{\"sequence\":1,\"type\":\"started\"}\n{\"type\":\"completed\"}\n", stream.String())

	reader := NewLLMHarnessJSONLReader(&stream, 128)
	var first struct {
		Type     string `json:"type"`
		Sequence int    `json:"sequence"`
	}
	require.NoError(t, reader.Decode(&first))
	assert.Equal(t, "started", first.Type)
	assert.Equal(t, 1, first.Sequence)

	second, err := reader.ReadRecord()
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"completed"}`, string(second))
	_, err = reader.ReadRecord()
	require.ErrorIs(t, err, io.EOF)
}

func TestLLMHarnessJSONLFramingErrors(t *testing.T) {
	t.Run("exact bound and CRLF", func(t *testing.T) {
		reader := NewLLMHarnessJSONLReader(strings.NewReader("{\"x\":1}\r\n"), len(`{"x":1}`))
		record, err := reader.ReadRecord()
		require.NoError(t, err)
		assert.Equal(t, `{"x":1}`, string(record))
	})

	t.Run("reader bound", func(t *testing.T) {
		reader := NewLLMHarnessJSONLReader(strings.NewReader("{\"too\":\"large\"}\n{\"ok\":1}\n"), 8)
		_, err := reader.ReadRecord()
		require.ErrorIs(t, err, ErrLLMHarnessJSONLRecordTooLarge)
		var framingErr *LLMHarnessJSONLError
		require.ErrorAs(t, err, &framingErr)
		assert.Equal(t, int64(1), framingErr.Record)
		assert.Equal(t, 8, framingErr.Max)

		// Oversize input is drained to preserve record framing.
		_, err = reader.ReadRecord()
		require.NoError(t, err)
	})

	t.Run("reader malformed", func(t *testing.T) {
		reader := NewLLMHarnessJSONLReader(strings.NewReader("not-json\n\n{}\n"), 64)
		_, err := reader.ReadRecord()
		require.ErrorIs(t, err, ErrLLMHarnessMalformedJSONL)
		_, err = reader.ReadRecord()
		require.ErrorIs(t, err, ErrLLMHarnessMalformedJSONL)
		var framingErr *LLMHarnessJSONLError
		require.ErrorAs(t, err, &framingErr)
		assert.Equal(t, int64(2), framingErr.Record)
		_, err = reader.ReadRecord()
		require.NoError(t, err)
	})

	t.Run("decode mismatch", func(t *testing.T) {
		reader := NewLLMHarnessJSONLReader(strings.NewReader("{\"sequence\":\"wrong\"}\n"), 64)
		var value struct {
			Sequence int `json:"sequence"`
		}
		err := reader.Decode(&value)
		require.ErrorIs(t, err, ErrLLMHarnessMalformedJSONL)
	})

	t.Run("writer bound", func(t *testing.T) {
		var stream bytes.Buffer
		writer := NewLLMHarnessJSONLWriter(&stream, 2)
		err := writer.WriteRecord(json.RawMessage(`{"x":1}`))
		require.ErrorIs(t, err, ErrLLMHarnessJSONLRecordTooLarge)
		assert.Empty(t, stream.String())
	})

	t.Run("writer malformed or multiline", func(t *testing.T) {
		var stream bytes.Buffer
		writer := NewLLMHarnessJSONLWriter(&stream, 64)
		for _, record := range []json.RawMessage{
			json.RawMessage(`{"x":`),
			json.RawMessage("{\n\"x\": 1\n}"),
		} {
			err := writer.WriteRecord(record)
			require.ErrorIs(t, err, ErrLLMHarnessMalformedJSONL)
		}
		assert.Empty(t, stream.String())
	})

	t.Run("marshal failure", func(t *testing.T) {
		writer := NewLLMHarnessJSONLWriter(io.Discard, 64)
		err := writer.Encode(func() {})
		require.ErrorIs(t, err, ErrLLMHarnessMalformedJSONL)
	})
}

func TestLLMHarnessEventVocabulary(t *testing.T) {
	events := []LLMHarnessEvent{
		LLMHarnessTurn{},
		LLMHarnessMessageLifecycle{},
		LLMHarnessTextDelta{},
		LLMHarnessThinkingDelta{},
		LLMHarnessToolCall{},
		LLMHarnessToolResult{},
		LLMHarnessUsage{},
		LLMHarnessCompleted{},
		LLMHarnessInterrupted{},
	}
	assert.Len(t, events, 9)

	assert.True(t, errors.Is(correlationError("one", "two", ErrLLMHarnessCorrelationConflict), ErrLLMHarnessCorrelationConflict))
}
