package daggercmd

import (
	"context"
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
