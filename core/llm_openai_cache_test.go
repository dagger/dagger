package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIPromptCacheKey(t *testing.T) {
	system := &LLMMessage{Role: LLMMessageRoleSystem, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "be terse"}}}
	prompt := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "review the PR"}}}
	reply := &LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{
		{Kind: LLMContentToolCall, CallID: "call_1", ToolName: "read", Arguments: JSON(`{"path":"a.go"}`)},
	}}
	result := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
		{Kind: LLMContentToolResult, CallID: "call_1", Text: "package a"},
	}}

	first := openAIPromptCacheKey([]*LLMMessage{system, prompt})
	require.NotEmpty(t, first)

	// The key must not move as the conversation grows: that is the whole
	// point of it.
	assert.Equal(t, first, openAIPromptCacheKey([]*LLMMessage{system, prompt, reply, result}))
	assert.Equal(t, first, openAIPromptCacheKey([]*LLMMessage{system, prompt, reply, result, reply, result}))

	// A different opening prompt is a different conversation.
	other := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "review the other PR"}}}
	assert.NotEqual(t, first, openAIPromptCacheKey([]*LLMMessage{system, other}))
	// So is the same prompt under different instructions.
	assert.NotEqual(t, first, openAIPromptCacheKey([]*LLMMessage{prompt}))
	// Media and tool blocks in the opening prompt count too.
	image := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
		{Kind: LLMContentText, Text: "review the PR"},
		{Kind: LLMContentImage, MIMEType: "image/png", Data: "aW1hZ2U="},
	}}
	assert.NotEqual(t, first, openAIPromptCacheKey([]*LLMMessage{system, image}))
}

func TestOpenAIChatSendsPromptCacheKey(t *testing.T) {
	requestBody := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requestBody <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)

	history := llmTestHistory()
	// A tool keeps the non-streaming path in use, which the stub answers.
	tools := []LLMTool{{Name: "read", Schema: map[string]any{"type": "object"}}}
	var payload struct {
		PromptCacheKey *string `json:"prompt_cache_key"`
	}

	client := newOpenAIClient(&LLMEndpoint{Model: "gpt-5", Provider: OpenAI, BaseURL: server.URL}, "", true)
	_, err := client.SendQuery(t.Context(), history, tools, &LLMCallOpts{})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(<-requestBody, &payload))
	require.NotNil(t, payload.PromptCacheKey)
	assert.Equal(t, openAIPromptCacheKey(history), *payload.PromptCacheKey)

	// Compatible endpoints get the plain request they always got.
	payload.PromptCacheKey = nil
	client = newOpenAIClient(&LLMEndpoint{Model: "llama", Provider: Local, BaseURL: server.URL}, "", true)
	_, err = client.SendQuery(t.Context(), history, tools, &LLMCallOpts{})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(<-requestBody, &payload))
	assert.Nil(t, payload.PromptCacheKey)
}

// codexStubRequest is what the stub backend saw of one Codex request.
type codexStubRequest struct {
	sessionID string
	turnState string
	body      []byte
}

// codexStubServer answers every Codex request with a minimal completed
// stream, issuing the given sticky-routing token, and records what it saw.
func codexStubServer(t *testing.T, issueTurnState func(n int) string) (*httptest.Server, func() []codexStubRequest) {
	t.Helper()
	var mu sync.Mutex
	var seen []codexStubRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		n := len(seen)
		seen = append(seen, codexStubRequest{
			sessionID: r.Header.Get("session-id"),
			turnState: r.Header.Get(codexTurnStateHeader),
			body:      body,
		})
		mu.Unlock()
		if token := issueTurnState(n); token != "" {
			w.Header().Set(codexTurnStateHeader, token)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.completed\n"+
			`data: {"type":"response.completed","sequence_number":1,"response":{"id":"resp","object":"response","status":"completed","output":[{"id":"msg","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}}`+
			"\n\n")
	}))
	t.Cleanup(server.Close)
	return server, func() []codexStubRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]codexStubRequest(nil), seen...)
	}
}

func TestCodexConversationAffinity(t *testing.T) {
	server, requests := codexStubServer(t, func(n int) string {
		if n == 0 {
			return "turn-1"
		}
		if n == 2 {
			return "turn-2"
		}
		// Later requests of a turn re-issue a token; the first one must win.
		return "ignored"
	})
	client := newOpenAICodexClient(&LLMEndpoint{
		Model: "openai-codex/gpt-5", Provider: OpenAICodex, BaseURL: server.URL, AuthToken: "tok",
	})

	system := &LLMMessage{Role: LLMMessageRoleSystem, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "be terse"}}}
	prompt := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "review the PR"}}}
	call := &LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{
		{Kind: LLMContentToolCall, CallID: "call_1", ToolName: "read", Arguments: JSON(`{}`)},
	}}
	result := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
		{Kind: LLMContentToolResult, CallID: "call_1", Text: "package a"},
	}}
	answer := &LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "looks fine"}}}
	followUp := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "and the tests?"}}}

	turns := [][]*LLMMessage{
		{system, prompt},                                               // turn 1, opens it
		{system, prompt, call, result},                                 // turn 1, tool round
		{system, prompt, call, result, answer, followUp},               // turn 2, opens it
		{system, prompt, call, result, answer, followUp, call, result}, // turn 2, tool round
	}
	for _, history := range turns {
		_, err := client.SendQuery(t.Context(), history, nil, &LLMCallOpts{})
		require.NoError(t, err)
	}

	seen := requests()
	require.Len(t, seen, len(turns))
	key := openAIPromptCacheKey(turns[0])
	for i, req := range seen {
		assert.Equal(t, key, req.sessionID, "request %d: the session id is the conversation's key", i)
		var payload struct {
			PromptCacheKey string `json:"prompt_cache_key"`
		}
		require.NoError(t, json.Unmarshal(req.body, &payload))
		assert.Equal(t, key, payload.PromptCacheKey, "request %d: the body carries the same key", i)
	}
	// The token issued at the start of a turn is replayed for the rest of
	// that turn, and never into the next one.
	assert.Empty(t, seen[0].turnState, "no token before the backend issues one")
	assert.Equal(t, "turn-1", seen[1].turnState)
	assert.Empty(t, seen[2].turnState, "a new prompt starts a new turn without the old token")
	assert.Equal(t, "turn-2", seen[3].turnState)
}

func TestCodexTurnIndex(t *testing.T) {
	system := &LLMMessage{Role: LLMMessageRoleSystem, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "be terse"}}}
	prompt := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "hi"}}}
	call := &LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentToolCall, CallID: "c", ToolName: "t"}}}
	result := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "c", Text: "ok"}}}
	// A prompt appended alongside tool results (a continuation steering the
	// model) opens a new turn too.
	mixed := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
		{Kind: LLMContentToolResult, CallID: "c", Text: "ok"},
		{Kind: LLMContentText, Text: "also do this"},
	}}

	assert.Equal(t, -1, codexTurnIndex([]*LLMMessage{system}))
	assert.Equal(t, 1, codexTurnIndex([]*LLMMessage{system, prompt}))
	assert.Equal(t, 1, codexTurnIndex([]*LLMMessage{system, prompt, call, result}))
	assert.Equal(t, 1, codexTurnIndex([]*LLMMessage{system, prompt, call, result, call, result}))
	assert.Equal(t, 3, codexTurnIndex([]*LLMMessage{system, prompt, call, mixed}))
}
