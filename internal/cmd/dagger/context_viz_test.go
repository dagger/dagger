package daggercmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func vizWindow(n int) *int { return &n }

// Match core.LLM.ToolsDoc's serialization without requiring an engine.
func vizToolDoc(t *testing.T, name, description, schema string) string {
	t.Helper()
	indented, err := json.MarshalIndent(json.RawMessage(schema), "", "  ")
	require.NoError(t, err)
	return fmt.Sprintf("## %s\n\n%s\n\n%s\n\n", name, description, indented)
}

func TestParseVizTools(t *testing.T) {
	schema := `{"type":"object","properties":{"path":{"type":"string","description":"A path with } and ## text"}},"required":["path"],"additionalProperties":false}`
	description := "Read a file.\n\n## Examples\n\n" +
		"{\n  \"path\": \"/tmp/file\"\n}\n\nThis is example JSON, not a schema.\n\n" +
		"```markdown\n" + vizToolDoc(t, "fake", "An example tool", schema) + "```\n\n" +
		"~~~~markdown\n" + vizToolDoc(t, "also_fake", "", "{}") + "~~~\n~~~~\n\n" +
		"## Notes\n\nPreserve trailing whitespace.  \n"
	for _, tc := range []struct {
		name string
		doc  string
		want []vizTool
	}{
		{name: "empty", want: []vizTool{}},
		{
			name: "multiple and bare",
			doc:  vizToolDoc(t, "read", "Read a file", schema) + vizToolDoc(t, "bare", "", "{}"),
			want: []vizTool{
				{Name: "read", Description: "Read a file", Schema: json.RawMessage(schema)},
				{Name: "bare", Schema: json.RawMessage(`{}`)},
			},
		},
		{
			name: "headings and examples in description",
			doc:  vizToolDoc(t, "read", description, schema) + vizToolDoc(t, "bare", "", "{}"),
			want: []vizTool{
				{Name: "read", Description: description, Schema: json.RawMessage(schema)},
				{Name: "bare", Schema: json.RawMessage(`{}`)},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools, err := parseVizTools(tc.doc)
			require.NoError(t, err)
			require.NotNil(t, tools)
			require.Len(t, tools, len(tc.want))
			for i, want := range tc.want {
				assert.Equal(t, want.Name, tools[i].Name)
				assert.Equal(t, want.Description, tools[i].Description)
				assert.JSONEq(t, string(want.Schema), string(tools[i].Schema))
				assert.Zero(t, tools[i].Calls)
			}
		})
	}
}

func TestParseVizToolsInvalid(t *testing.T) {
	valid := vizToolDoc(t, "valid", "", "{}")
	for _, doc := range []string{
		" ",
		"not tool documentation",
		"## \n\n\n\n{}\n\n",
		"## missing-schema\n\nDescription only\n\n",
		"## array\n\n\n\n[]\n\n",
		"## null\n\n\n\nnull\n\n",
		"## invalid\n\n\n\n{broken}\n\n",
		"## compact\n\n\n\n{\"type\":\"object\"}\n\n",
		"## indented\n\n\n\n{\n    \"type\": \"object\"\n}\n\n",
		strings.TrimSuffix(valid, "\n"),
		valid + "unexpected suffix",
		valid + "## broken\n\nNo schema\n\n",
		valid + valid,
	} {
		t.Run(doc, func(t *testing.T) {
			tools, err := parseVizTools(doc)
			require.Error(t, err)
			assert.Empty(t, tools, "never publish a partial inventory")

			snap := buildContextVizSnapshot(&vizConversation{Tools: doc})
			assert.NotEmpty(t, snap.ToolsError)
			assert.Equal(t, []vizTool{}, snap.Tools)
			require.Len(t, snap.Items, 1)
			assert.Equal(t, "Tool schemas", snap.Items[0].Label)
			assert.Equal(t, doc, snap.Items[0].Text)
			assert.Equal(t, vizEstimateTokens(len(doc)), snap.Items[0].Tokens)
			data, err := json.Marshal(snap)
			require.NoError(t, err)
			assert.Contains(t, string(data), `"tools":[]`)
			assert.Contains(t, string(data), `"toolsError":`)
		})
	}
}

func TestBuildContextVizSnapshotToolInventory(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		snap := buildContextVizSnapshot(&vizConversation{})
		assert.Equal(t, []vizTool{}, snap.Tools)
		assert.Empty(t, snap.ToolsError)
		assert.Empty(t, snap.Items)
		data, err := json.Marshal(snap)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"tools":[]`)
		assert.Contains(t, string(data), `"items":[]`)
		assert.Contains(t, string(data), `"calls":[]`)
		assert.NotContains(t, string(data), `"toolsError"`)
	})

	schema := `{"type":"object","properties":{"size":{"type":"integer","maximum":9007199254740993},"nested":{"anyOf":[{"type":"string"},{"type":"null"}]}},"required":["size"]}`
	doc := vizToolDoc(t, "read", "## Notes\n\nRead a file", schema) +
		vizToolDoc(t, "other", "", "{}") + vizToolDoc(t, "unused", "", "{}")
	conv := &vizConversation{
		Tools: doc,
		Messages: []vizMessage{
			{Role: "ASSISTANT", Content: []vizBlock{
				{Kind: "TOOL_CALL", ToolName: "read", CallID: "1", Arguments: "{}"},
				{Kind: "TOOL_CALL", ToolName: "read", CallID: "2", Arguments: "{}"},
				{Kind: "TOOL_CALL", ToolName: "other", CallID: "3", Arguments: "{}"},
				{Kind: "TOOL_CALL", ToolName: "removed", CallID: "4", Arguments: "{}"},
				{Kind: "TEXT", Text: "read read", ToolName: "read"},
			}},
			{Role: "USER", Content: []vizBlock{
				{Kind: "TOOL_RESULT", ToolName: "read", CallID: "1", Text: "failed", Errored: true},
				{Kind: "TOOL_RESULT", ToolName: "read", CallID: "2", Text: "ok"},
			}},
		},
	}
	snap := buildContextVizSnapshot(conv)
	require.Empty(t, snap.ToolsError)
	require.Len(t, snap.Tools, 3)
	assert.Equal(t, 2, snap.Tools[0].Calls)
	assert.Equal(t, 1, snap.Tools[1].Calls)
	assert.Zero(t, snap.Tools[2].Calls)
	assert.Equal(t, "Tool schemas (3 tools)", snap.Items[0].Label)
	assert.Equal(t, doc, snap.Items[0].Text)
	assert.True(t, snap.Items[0].Fixed)
	assert.Equal(t, vizEstimateTokens(len(doc)), vizFindCategory(t, snap, vizCatTools).Tokens)
	require.Len(t, snap.Items, 8, "inventory must not add transcript entries or tokens")
	var total int64
	for _, item := range snap.Items {
		total += item.Tokens
	}
	assert.Equal(t, total, snap.ClassifiedTokens)

	data, err := json.Marshal(snap)
	require.NoError(t, err)
	var restored contextVizSnapshot
	require.NoError(t, json.Unmarshal(data, &restored))
	assert.JSONEq(t, schema, string(restored.Tools[0].Schema))
	assert.Contains(t, string(restored.Tools[0].Schema), "9007199254740993", "schema numbers must not pass through float64")
	assert.Equal(t, doc, restored.Items[0].Text)
	assert.Equal(t, 2, restored.Tools[0].Calls)
}

// vizFindCategory returns the total for a category ID.
func vizFindCategory(t *testing.T, snap *contextVizSnapshot, id string) vizCategory {
	t.Helper()
	for _, cat := range snap.Categories {
		if cat.ID == id {
			return cat
		}
	}
	t.Fatalf("category %q not found", id)
	return vizCategory{}
}

func TestBuildContextVizSnapshotCalibration(t *testing.T) {
	// One API call measuring the context, then a trailing tool result the
	// provider has not seen yet. The measured context must be distributed
	// over the blocks that produced it, proportionally to their chars/4
	// estimates, with the response's own blocks pinned to outputTokens.
	conv := &vizConversation{
		Agent:         "interactive",
		AutoCompact:   true,
		Model:         "claude-fable-5",
		Provider:      "anthropic",
		ContextWindow: vizWindow(1000000),
		// Measured: call 1 occupies 2000; the trailing 800-char tool result
		// estimates to 200 more. (Mirrors core.estimateOccupiedContextTokens.)
		ContextTokens: 2200,
		Tools:         "## read\n\nRead a file\n" + strings.Repeat("s", 19), // 40 chars → 10 raw
		Messages: []vizMessage{
			{
				Role:    "SYSTEM",
				Content: []vizBlock{{Kind: "TEXT", Text: strings.Repeat("s", 400)}}, // 100 raw
			},
			{
				Role:    "USER",
				Content: []vizBlock{{Kind: "TEXT", Text: strings.Repeat("u", 200)}}, // 50 raw
			},
			{
				Role: "ASSISTANT",
				Content: []vizBlock{
					{Kind: "TEXT", Text: "on it"},
					{Kind: "TOOL_CALL", CallID: "call-1", ToolName: "read", Arguments: `{"path":"/x"}`},
				},
				TokenUsage: vizUsage{
					InputTokens: 1000, OutputTokens: 60,
					CachedTokenWrites: 940, TotalTokens: 2000,
				},
			},
			{
				Role: "USER",
				Content: []vizBlock{
					{Kind: "TOOL_RESULT", CallID: "call-1", Text: strings.Repeat("r", 800)}, // 200 raw
				},
			},
		},
	}

	snap := buildContextVizSnapshot(conv)

	// Window raw estimates: tools 10 + system 100 + user 50 = 160. The call
	// measured 2000 occupied, of which 60 is the response's own output, so
	// 1940 spreads over the window: 1940*10/160=121, 1940*100/160=1212, and
	// the last item absorbs the remainder: 607.
	assert.Equal(t, int64(121), vizFindCategory(t, snap, vizCatTools).Tokens)
	assert.Equal(t, int64(1212), vizFindCategory(t, snap, vizCatSystem).Tokens)
	assert.Equal(t, int64(607), vizFindCategory(t, snap, vizCatUser).Tokens)

	// The response's blocks split its exact outputTokens: TEXT "on it" (2
	// raw) and TOOL_CALL (5 raw): 60*2/7=17, remainder 43.
	assert.Equal(t, int64(17), vizFindCategory(t, snap, vizCatAssistant).Tokens)
	assert.Equal(t, int64(43), vizFindCategory(t, snap, vizCatToolCall).Tokens)

	// The trailing tool result keeps its raw estimate, flagged as such.
	trailing := snap.Items[len(snap.Items)-1]
	assert.Equal(t, vizCatToolResult, trailing.Category)
	assert.Equal(t, int64(200), trailing.Tokens)
	assert.True(t, trailing.Est)

	// Everything the call measured is calibrated, not estimated.
	for _, item := range snap.Items[:len(snap.Items)-1] {
		assert.False(t, item.Est, "item %d (%s) should be calibrated", item.Index, item.Label)
	}

	// The classified total telescopes to the measured context: nothing is
	// left unattributed.
	assert.Equal(t, int64(2200), snap.ClassifiedTokens)
	assert.Zero(t, vizFindCategory(t, snap, vizCatOverhead).Tokens)

	// Items carry stable, position-matching indexes (the UI keys on them).
	for i, item := range snap.Items {
		assert.Equal(t, i, item.Index)
	}
	require.Len(t, snap.Calls, 1)
	assert.False(t, snap.Calls[0].CacheMiss, "the first call is a cold cache, not a miss")
}

func TestBuildContextVizSnapshotMultiCallWindows(t *testing.T) {
	// Two calls: each window's growth lands on the blocks added in between,
	// and a ReadSkill result classifies as a skill read.
	conv := &vizConversation{
		ContextTokens: 1600,
		Messages: []vizMessage{
			{
				Role:    "USER",
				Content: []vizBlock{{Kind: "TEXT", Text: strings.Repeat("u", 100)}},
			},
			{
				Role: "ASSISTANT",
				Content: []vizBlock{
					{Kind: "TOOL_CALL", CallID: "c1", ToolName: "ReadSkill", Arguments: `{"name":"x"}`},
				},
				TokenUsage: vizUsage{InputTokens: 900, OutputTokens: 100, TotalTokens: 1000},
			},
			{
				Role: "USER",
				Content: []vizBlock{
					{Kind: "TOOL_RESULT", CallID: "c1", Text: strings.Repeat("k", 2000)},
				},
			},
			{
				Role:       "ASSISTANT",
				Content:    []vizBlock{{Kind: "TEXT", Text: "done"}},
				TokenUsage: vizUsage{InputTokens: 1450, OutputTokens: 50, TotalTokens: 1600},
			},
		},
	}

	snap := buildContextVizSnapshot(conv)

	// Window 1: the user prompt is the only prompt-side block → it gets the
	// whole 900; the ReadSkill call gets call 1's output.
	assert.Equal(t, int64(900), vizFindCategory(t, snap, vizCatUser).Tokens)
	assert.Equal(t, int64(100), vizFindCategory(t, snap, vizCatToolCall).Tokens)

	// Window 2: growth 1600-1000=600, minus call 2's output 50 → the skill
	// read measures 550 (not its 500 raw estimate).
	assert.Equal(t, int64(550), vizFindCategory(t, snap, vizCatSkills).Tokens)
	assert.Equal(t, int64(50), vizFindCategory(t, snap, vizCatAssistant).Tokens)

	assert.Equal(t, int64(1600), snap.ClassifiedTokens)
	assert.Zero(t, vizFindCategory(t, snap, vizCatOverhead).Tokens)
}

func TestBuildContextVizSnapshotUncalibrated(t *testing.T) {
	// No API call yet: blocks keep raw estimates and the measured gap
	// surfaces as unattributed rather than being hidden.
	conv := &vizConversation{
		ContextTokens: 500,
		Tools:         strings.Repeat("t", 40), // 10 raw
		Messages: []vizMessage{
			{Role: "SYSTEM", Content: []vizBlock{{Kind: "TEXT", Text: strings.Repeat("s", 400)}}}, // 100 raw
			{Role: "USER", Content: []vizBlock{{Kind: "TEXT", Text: strings.Repeat("u", 200)}}},   // 50 raw
		},
	}
	snap := buildContextVizSnapshot(conv)
	for _, item := range snap.Items {
		assert.True(t, item.Est)
	}
	assert.Equal(t, int64(10), vizFindCategory(t, snap, vizCatTools).Tokens)
	assert.Equal(t, int64(100), vizFindCategory(t, snap, vizCatSystem).Tokens)
	assert.Equal(t, int64(50), vizFindCategory(t, snap, vizCatUser).Tokens)
	assert.Equal(t, int64(340), vizFindCategory(t, snap, vizCatOverhead).Tokens)
	assert.Equal(t, int64(500), snap.ClassifiedTokens)
}

func TestBuildContextVizSnapshotUnattributedGrowth(t *testing.T) {
	// A call whose measured growth exceeds its output with no prompt-side
	// blocks to carry it: the residue lands in the unattributed bucket
	// instead of vanishing.
	conv := &vizConversation{
		ContextTokens: 500,
		Messages: []vizMessage{
			{
				Role:       "ASSISTANT",
				Content:    []vizBlock{{Kind: "TEXT", Text: "hi"}},
				TokenUsage: vizUsage{InputTokens: 400, OutputTokens: 100, TotalTokens: 500},
			},
		},
	}
	snap := buildContextVizSnapshot(conv)
	assert.Equal(t, int64(100), vizFindCategory(t, snap, vizCatAssistant).Tokens)
	assert.Equal(t, int64(400), vizFindCategory(t, snap, vizCatOverhead).Tokens)
	assert.Equal(t, int64(500), snap.ClassifiedTokens)
}

func TestBuildContextVizSnapshotStagnantContext(t *testing.T) {
	// A call that reports no context growth (e.g. restored history with
	// partial usage) leaves its window estimated; a later call's delta
	// covers the whole span.
	conv := &vizConversation{
		ContextTokens: 1000,
		Messages: []vizMessage{
			{Role: "USER", Content: []vizBlock{{Kind: "TEXT", Text: strings.Repeat("u", 400)}}}, // 100 raw
			{
				Role:       "ASSISTANT",
				Content:    []vizBlock{{Kind: "TEXT", Text: strings.Repeat("a", 400)}}, // 100 raw
				TokenUsage: vizUsage{InputTokens: 500, TotalTokens: 500},
			},
			{
				// Reports the SAME occupied context: no measurable growth.
				Role:       "ASSISTANT",
				Content:    []vizBlock{{Kind: "TEXT", Text: strings.Repeat("b", 400)}}, // 100 raw
				TokenUsage: vizUsage{InputTokens: 500, TotalTokens: 500},
			},
			{
				Role:       "ASSISTANT",
				Content:    []vizBlock{{Kind: "TEXT", Text: strings.Repeat("c", 400)}}, // 100 raw
				TokenUsage: vizUsage{InputTokens: 900, OutputTokens: 100, TotalTokens: 1000},
			},
		},
	}
	snap := buildContextVizSnapshot(conv)
	// Call 1 calibrates the first window (user 400, assistant 100... the
	// user prompt gets delta-output = 500-0 = 500, assistant block 0).
	// Call 2's delta is 0, so its block joins the window; call 3's delta
	// (500) then covers call 2's block (spread 400) plus its own output 100.
	assert.Equal(t, int64(1000), snap.ClassifiedTokens)
	assert.Zero(t, vizFindCategory(t, snap, vizCatOverhead).Tokens)
	for _, item := range snap.Items {
		assert.False(t, item.Est, "item %d should be calibrated by a later call", item.Index)
	}
	require.Len(t, snap.Calls, 3)
}

func TestBuildContextVizSnapshotCacheMiss(t *testing.T) {
	assistant := func(reads, writes int64) vizMessage {
		return vizMessage{
			Role:    "ASSISTANT",
			Content: []vizBlock{{Kind: "TEXT", Text: "ok"}},
			TokenUsage: vizUsage{
				InputTokens: 10, OutputTokens: 10,
				CachedTokenReads: reads, CachedTokenWrites: writes,
				TotalTokens: 20 + reads + writes,
			},
		}
	}
	user := vizMessage{
		Role:    "USER",
		Content: []vizBlock{{Kind: "TEXT", Text: "hi"}},
	}

	conv := &vizConversation{
		ContextTokens: 100,
		Messages: []vizMessage{
			user,
			assistant(0, 10000), // cold: everything written
			user,
			assistant(10000, 500), // warm: reads back what was cached
			user,
			assistant(2000, 9000), // miss: reads far less than was cached
		},
	}

	snap := buildContextVizSnapshot(conv)
	require.Len(t, snap.Calls, 3)
	assert.False(t, snap.Calls[0].CacheMiss)
	assert.False(t, snap.Calls[1].CacheMiss)
	assert.True(t, snap.Calls[2].CacheMiss)
}

func TestBuildContextVizSnapshotNoCacheReporting(t *testing.T) {
	// Providers that report no cache activity must not flag misses.
	assistant := vizMessage{
		Role:       "ASSISTANT",
		Content:    []vizBlock{{Kind: "TEXT", Text: "ok"}},
		TokenUsage: vizUsage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110},
	}
	conv := &vizConversation{
		ContextTokens: 100,
		Messages:      []vizMessage{assistant, assistant, assistant},
	}
	snap := buildContextVizSnapshot(conv)
	require.Len(t, snap.Calls, 3)
	for _, call := range snap.Calls {
		assert.False(t, call.CacheMiss)
	}
}

func TestBuildContextVizSnapshotEmptyConversation(t *testing.T) {
	snap := buildContextVizSnapshot(&vizConversation{Agent: "interactive"})
	assert.Empty(t, snap.Items)
	assert.Empty(t, snap.Calls)
	assert.Zero(t, snap.ContextWindow)
	assert.Zero(t, snap.ReserveTokens)
	// Categories are always present so the UI's legend is stable.
	assert.Len(t, snap.Categories, len(vizCategoryOrder))
}

func TestContextVizMux(t *testing.T) {
	srv := httptest.NewServer(contextVizMux(func(context.Context) (*contextVizSnapshot, error) {
		return buildContextVizSnapshot(&vizConversation{
			Agent:         "interactive",
			Model:         "some-model",
			ContextTokens: 42,
		}), nil
	}))
	defer srv.Close()

	// The page is served at / only.
	res, err := http.Get(srv.URL + "/")
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "text/html")

	res, err = http.Get(srv.URL + "/nope")
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)

	res, err = http.Get(srv.URL + "/snapshot.json")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "application/json")
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"model":"some-model"`)
	assert.Contains(t, string(body), `"contextTokens":42`)
}

func TestContextVizMuxSnapshotError(t *testing.T) {
	srv := httptest.NewServer(contextVizMux(func(context.Context) (*contextVizSnapshot, error) {
		return nil, fmt.Errorf("no conversation yet")
	}))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/snapshot.json")
	require.NoError(t, err)
	res.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
}

func TestVizLabel(t *testing.T) {
	assert.Equal(t, "hello", vizLabel("hello\nworld", 80))
	assert.Equal(t, "(empty)", vizLabel("  \n\n", 80))
	long := strings.Repeat("x", 200)
	label := vizLabel(long, 80)
	assert.LessOrEqual(t, len([]rune(label)), 80)
	assert.True(t, strings.HasSuffix(label, "…"))
}
