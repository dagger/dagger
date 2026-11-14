package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// timeoutOrder is the order Timeout offers its arguments in: the wrapped tool
// first (see ToolSchemaProperties).
var timeoutOrder = []string{"tool", "duration", "arguments"}

// jsonObjectKeys returns a JSON object's keys in the order they're written.
func jsonObjectKeys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	require.NoError(t, err)
	require.Equal(t, json.Delim('{'), tok, "not an object: %s", raw)
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		require.NoError(t, err)
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		require.NoError(t, dec.Decode(&skip))
	}
	return keys
}

// toolSchemaOf finds a tool's schema in a request body's tools: under
// schemaKey of each tool, beside its "name" -- or of the object under within,
// if set, as OpenAI nests them under "function".
func toolSchemaOf(t *testing.T, body []byte, name, within, schemaKey string) map[string]json.RawMessage {
	t.Helper()
	var payload struct {
		Tools []map[string]json.RawMessage `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(body, &payload), "%s", body)
	for _, tool := range payload.Tools {
		if within != "" {
			var inner map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(tool[within], &inner))
			tool = inner
		}
		var toolName string
		require.NoError(t, json.Unmarshal(tool["name"], &toolName))
		if toolName != name {
			continue
		}
		var schema map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(tool[schemaKey], &schema))
		return schema
	}
	t.Fatalf("no tool %q in request: %s", name, body)
	return nil
}

// assertTimeoutOrder asserts a request's Timeout schema offers its
// properties, and lists its required ones, in timeoutOrder.
func assertTimeoutOrder(t *testing.T, schema map[string]json.RawMessage) {
	t.Helper()
	assert.Equal(t, timeoutOrder, jsonObjectKeys(t, schema["properties"]))
	var required []string
	require.NoError(t, json.Unmarshal(schema["required"], &required))
	assert.Equal(t, timeoutOrder, required)
}

func TestToolSchemaPropertiesMarshal(t *testing.T) {
	props := ToolSchemaProperties{
		{Name: "zeta", Schema: map[string]any{"type": "string"}},
		{Name: "alpha", Schema: map[string]any{"type": "object", "properties": map[string]any{}}},
		{Name: `quo"te`, Schema: map[string]any{"description": "<b>"}},
	}
	raw, err := json.Marshal(map[string]any{"properties": props})
	require.NoError(t, err)
	var schema map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &schema))
	assert.Equal(t, []string{"zeta", "alpha", `quo"te`}, jsonObjectKeys(t, schema["properties"]))

	// Unordered, it's the same object.
	unordered, err := json.Marshal(props.Map())
	require.NoError(t, err)
	assert.JSONEq(t, string(unordered), string(schema["properties"]))

	m, ok := schemaPropertiesMap(props)
	require.True(t, ok)
	assert.Equal(t, props.Map(), m)
	_, ok = schemaPropertiesMap("nope")
	assert.False(t, ok)
}

// TestAnthropicToolSchemaOrder covers Timeout's argument order reaching
// Anthropic's request body: the SDK marshals the properties as given.
func TestAnthropicToolSchemaOrder(t *testing.T) {
	timeout := timeoutToolForTest(t)
	bodies := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies <- body
		// Not retried by the SDK; only the request is under test.
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	client := newAnthropicClient(&LLMEndpoint{Provider: Anthropic, Key: "key", BaseURL: srv.URL, Model: "claude-test"})
	_, err := client.SendQuery(t.Context(), llmTestHistory(), []LLMTool{*timeout}, &LLMCallOpts{})
	require.Error(t, err, "test server deliberately rejects the request")
	assertTimeoutOrder(t, toolSchemaOf(t, <-bodies, timeoutToolName, "", "input_schema"))
}

// TestOpenAIToolSchemaOrder covers Timeout's argument order reaching OpenAI's
// chat completions request body.
func TestOpenAIToolSchemaOrder(t *testing.T) {
	timeout := timeoutToolForTest(t)
	bodies := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	client := newOpenAIClient(&LLMEndpoint{Model: "gpt-5", Provider: OpenAI, BaseURL: srv.URL}, "", true)
	_, err := client.SendQuery(t.Context(), llmTestHistory(), []LLMTool{*timeout}, &LLMCallOpts{})
	require.NoError(t, err)
	assertTimeoutOrder(t, toolSchemaOf(t, <-bodies, timeoutToolName, "function", "parameters"))
}

// TestCodexToolSchemaOrder covers Timeout's argument order reaching the
// Codex responses request body.
func TestCodexToolSchemaOrder(t *testing.T) {
	timeout := timeoutToolForTest(t)
	srv, requests := codexStubServer(t, func(int) string { return "" })
	client := newOpenAICodexClient(&LLMEndpoint{
		Model: "openai-codex/gpt-5", Provider: OpenAICodex, BaseURL: srv.URL, AuthToken: "tok",
	})
	_, err := client.SendQuery(t.Context(), llmTestHistory(), []LLMTool{*timeout}, &LLMCallOpts{})
	require.NoError(t, err)
	seen := requests()
	require.Len(t, seen, 1)
	assertTimeoutOrder(t, toolSchemaOf(t, seen[0].body, timeoutToolName, "", "parameters"))
}

// timeoutCallArgs is a Timeout call the model wrote in the order the schema
// offers, which sorting would turn around.
const timeoutCallArgs = `{"tool":"read","duration":"1m","arguments":{"path":"/x","offset":2}}`

// timeoutCallHistory is a conversation in which the model called Timeout.
func timeoutCallHistory(args string) []*LLMMessage {
	return []*LLMMessage{
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "read it"}}},
		{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentToolCall, CallID: "call-1", ToolName: timeoutToolName, Arguments: JSON(args)}}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "call-1", Text: "contents"}}},
	}
}

// TestProviderReplaysToolCallArguments covers resubmitting a past tool call's
// arguments exactly as the model wrote them, rather than re-marshaled with
// sorted keys, which would show the model its own Timeout call rewritten
// arguments-first.
func TestProviderReplaysToolCallArguments(t *testing.T) {
	t.Run("anthropic", func(t *testing.T) {
		client, requests := mediaRequestClient(t, "anthropic")
		request := sentMediaRequest(t, client, requests, timeoutCallHistory(timeoutCallArgs))
		var messages []struct {
			Content []struct {
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		}
		require.NoError(t, json.Unmarshal(request["messages"], &messages))
		require.Len(t, messages, 3)
		assert.Equal(t, timeoutCallArgs, string(messages[1].Content[0].Input))
	})

	t.Run("openai", func(t *testing.T) {
		bodies := make(chan []byte, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			bodies <- body
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
		}))
		t.Cleanup(srv.Close)
		client := newOpenAIClient(&LLMEndpoint{Model: "gpt-5", Provider: OpenAI, BaseURL: srv.URL}, "", true)
		// Only the request is under test.
		_, _ = client.SendQuery(t.Context(), timeoutCallHistory(timeoutCallArgs), nil, &LLMCallOpts{})
		var payload struct {
			Messages []struct {
				ToolCalls []struct {
					Function struct {
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		require.NoError(t, json.Unmarshal(<-bodies, &payload))
		var replayed []string
		for _, msg := range payload.Messages {
			for _, call := range msg.ToolCalls {
				replayed = append(replayed, call.Function.Arguments)
			}
		}
		assert.Equal(t, []string{timeoutCallArgs}, replayed)
	})

	t.Run("codex", func(t *testing.T) {
		srv, requests := codexStubServer(t, func(int) string { return "" })
		client := newOpenAICodexClient(&LLMEndpoint{
			Model: "openai-codex/gpt-5", Provider: OpenAICodex, BaseURL: srv.URL, AuthToken: "tok",
		})
		_, err := client.SendQuery(t.Context(), timeoutCallHistory(timeoutCallArgs), nil, &LLMCallOpts{})
		require.NoError(t, err)
		seen := requests()
		require.Len(t, seen, 1)
		var payload struct {
			Input []struct {
				Type      string `json:"type"`
				Arguments string `json:"arguments"`
			} `json:"input"`
		}
		require.NoError(t, json.Unmarshal(seen[0].body, &payload))
		var replayed []string
		for _, item := range payload.Input {
			if item.Type == "function_call" {
				replayed = append(replayed, item.Arguments)
			}
		}
		assert.Equal(t, []string{timeoutCallArgs}, replayed)
	})
}

func TestToolCallInput(t *testing.T) {
	for _, tc := range []struct {
		name, args, want string
	}{
		{"object keeps its key order", timeoutCallArgs, timeoutCallArgs},
		{"surrounding whitespace is trimmed", " \n" + timeoutCallArgs + "\n", timeoutCallArgs},
		{"no arguments resubmit as an empty object", "", `{}`},
		{"truncated object is wrapped", `{"tool":"re`, `{"INVALID_JSON":"{\"tool\":\"re"}`},
		{"array is wrapped", `[1]`, `{"INVALID_JSON":"[1]"}`},
		{"null is wrapped", `null`, `{"INVALID_JSON":"null"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, string(toolCallInput(JSON(tc.args))))
		})
	}
	assert.Equal(t, map[string]any{"INVALID_JSON": "[1]"}, toolCallInputMap(JSON(`[1]`)))
	assert.Equal(t, map[string]any{}, toolCallInputMap(nil))
}

// TestMCPToolSchemaOrder covers Timeout's argument order reaching an MCP
// client, in tools/list.
func TestMCPToolSchemaOrder(t *testing.T) {
	timeout := timeoutToolForTest(t)
	tool, err := genMcpTool(*timeout)
	require.NoError(t, err)
	raw, err := json.Marshal(map[string]any{"tools": []any{tool}})
	require.NoError(t, err)
	assertTimeoutOrder(t, toolSchemaOf(t, raw, timeoutToolName, "", "inputSchema"))
}

// TestGenaiToolSchemaOrder covers Timeout's argument order reaching Gemini:
// its typed schema keeps properties in a map, so the order goes along as
// propertyOrdering.
func TestGenaiToolSchemaOrder(t *testing.T) {
	timeout := timeoutToolForTest(t)
	schema, err := bbiSchemaToGenaiSchema(timeout.Schema)
	require.NoError(t, err)
	require.Len(t, schema.Properties, 3)
	for _, name := range timeoutOrder {
		require.Contains(t, schema.Properties, name)
	}
	assert.Equal(t, "Name of the currently exposed tool to invoke.", schema.Properties["tool"].Description)
	raw, err := json.Marshal(schema)
	require.NoError(t, err)
	var sent struct {
		PropertyOrdering []string `json:"propertyOrdering"`
		Required         []string `json:"required"`
	}
	require.NoError(t, json.Unmarshal(raw, &sent))
	assert.Equal(t, timeoutOrder, sent.PropertyOrdering)
	assert.Equal(t, timeoutOrder, sent.Required)

	// A tool listing its properties in a map leaves the order to Gemini.
	plain, err := bbiSchemaToGenaiSchema(map[string]any{
		"type":       "object",
		"properties": map[string]any{"a": map[string]any{"type": "string"}},
	})
	require.NoError(t, err)
	assert.Nil(t, plain.PropertyOrdering)
}
