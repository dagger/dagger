package daggercmd

// The context visualizer: a local web UI that renders the focused
// conversation's context-window usage, in the spirit of Claude Code's
// /context view.
//
// It classifies everything the engine exposes about the conversation --
// system prompts, tool schemas, skill reads, user prompts, assistant
// replies, thinking, tool calls and tool results -- into a stacked
// breakdown bar, renders the transcript with a per-block token count
// (calibrated against the provider-reported usage of each API call, so the
// buckets add up to the measured context), and charts per-API-call cache
// activity so a cache miss (e.g. a system prompt or toolset change
// invalidating the provider's prompt cache) is visible at a glance.
//
// The server starts lazily on the ctrl+t hotkey (or the .context builtin),
// listens on a random localhost port for the rest of the session, and
// always describes the conversation the prompt is FOCUSED on -- refreshing
// the page after a focus switch shows the newly focused agent.

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/pkg/browser"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
)

//go:embed context_viz.html
var contextVizHTML []byte

// contextVizQuery reads everything the visualizer classifies in a single
// round trip: the conversation's structured message history (with the
// provider-reported token usage on each assistant response), the rendered
// tool schemas, the skills index, and the context-window accounting.
const contextVizQuery = `query ContextViz($id: ID!) {
  node(id: $id) {
    ... on LLM {
      model
      provider
      contextWindow
      contextTokens
      tools
      skills {
        name
        description
      }
      messages {
        role
        content {
          kind
          text
          callId
          toolName
          arguments
          errored
        }
        tokenUsage {
          inputTokens
          outputTokens
          cachedTokenReads
          cachedTokenWrites
          totalTokens
        }
      }
    }
  }
}`

// vizConversation is the raw engine snapshot the classifier consumes.
type vizConversation struct {
	Agent         string
	AutoCompact   bool
	Model         string       `json:"model"`
	Provider      string       `json:"provider"`
	ContextWindow *int         `json:"contextWindow"`
	ContextTokens int          `json:"contextTokens"`
	Tools         string       `json:"tools"`
	Skills        []vizSkill   `json:"skills"`
	Messages      []vizMessage `json:"messages"`
}

type vizSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type vizMessage struct {
	Role       string     `json:"role"`
	Content    []vizBlock `json:"content"`
	TokenUsage vizUsage   `json:"tokenUsage"`
}

type vizBlock struct {
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	CallID    string `json:"callId"`
	ToolName  string `json:"toolName"`
	Arguments string `json:"arguments"`
	Errored   bool   `json:"errored"`
}

type vizUsage struct {
	InputTokens       int64 `json:"inputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
	CachedTokenReads  int64 `json:"cachedTokenReads"`
	CachedTokenWrites int64 `json:"cachedTokenWrites"`
	TotalTokens       int64 `json:"totalTokens"`
}

func (u vizUsage) hasTokens() bool {
	return u.InputTokens != 0 || u.OutputTokens != 0 ||
		u.CachedTokenReads != 0 || u.CachedTokenWrites != 0 || u.TotalTokens != 0
}

func (u vizUsage) hasCacheActivity() bool {
	return u.CachedTokenReads != 0 || u.CachedTokenWrites != 0
}

// contextTokens mirrors core.LLMTokenUsage.contextTokens: the context the
// conversation occupies as of this API call. Providers should fill
// TotalTokens as the sum of the additive buckets; the max keeps native
// totals that include extra categories (e.g. reasoning) from truncating.
func (u vizUsage) contextTokens() int64 {
	components := u.InputTokens + u.OutputTokens + u.CachedTokenReads + u.CachedTokenWrites
	return max(u.TotalTokens, components)
}

// contextVizSnapshot is what /snapshot.json serves: the classified view of
// one conversation at one moment.
type contextVizSnapshot struct {
	Agent            string        `json:"agent"`
	Model            string        `json:"model"`
	Provider         string        `json:"provider"`
	ContextWindow    int           `json:"contextWindow"`
	ContextTokens    int           `json:"contextTokens"`
	ClassifiedTokens int64         `json:"classifiedTokens"`
	ReserveTokens    int           `json:"reserveTokens"`
	AutoCompact      bool          `json:"autoCompact"`
	GeneratedAt      string        `json:"generatedAt"`
	Skills           []vizSkill    `json:"skills,omitempty"`
	Categories       []vizCategory `json:"categories"`
	Items            []vizItem     `json:"items"`
	Calls            []vizCall     `json:"calls"`
}

// vizCategory is one classification bucket, totalled across the items that
// belong to it.
type vizCategory struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Tokens int64  `json:"tokens"`
}

// vizItem is one transcript entry: a content block of the conversation (or a
// synthetic fixed-context entry such as the tool schemas), carrying its token
// estimate and the category it was classified into.
type vizItem struct {
	Index    int    `json:"index"`
	Category string `json:"category"`
	Role     string `json:"role,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Label    string `json:"label"`
	Tokens   int64  `json:"tokens"`
	Text     string `json:"text,omitempty"`
	ToolName string `json:"toolName,omitempty"`
	Errored  bool   `json:"errored,omitempty"`
	// Fixed marks synthetic entries that occupy context on every API call
	// (e.g. tool schemas) rather than transcript content.
	Fixed bool `json:"fixed,omitempty"`
	// Call is the 1-based API-call index for blocks of an assistant
	// response, correlating the transcript with the cache chart.
	Call int `json:"call,omitempty"`
	// Est marks a raw chars/4 estimate. Everything else is calibrated
	// against provider-reported usage (see the calibration pass in
	// buildContextVizSnapshot).
	Est bool `json:"est,omitempty"`
}

// vizCall is one provider API call (an assistant response with reported
// usage), for the cache-activity chart.
type vizCall struct {
	Index       int   `json:"index"` // 1-based
	Item        int   `json:"item"`  // index of the response's first item
	Input       int64 `json:"input"` // uncached input tokens
	Output      int64 `json:"output"`
	CacheReads  int64 `json:"cacheReads"`
	CacheWrites int64 `json:"cacheWrites"`
	// CacheMiss flags a call whose cache reads fell short of what the
	// previous call had cached -- the provider re-ingested part of the
	// prompt prefix (e.g. after a system-prompt or toolset change).
	CacheMiss bool `json:"cacheMiss"`
}

// Category IDs, in the order the breakdown bar stacks them.
const (
	vizCatSystem     = "system"
	vizCatTools      = "tools"
	vizCatSkills     = "skills"
	vizCatUser       = "user"
	vizCatAssistant  = "assistant"
	vizCatThinking   = "thinking"
	vizCatToolCall   = "tool-call"
	vizCatToolResult = "tool-result"
	vizCatOverhead   = "overhead"
)

var vizCategoryOrder = []vizCategory{
	{ID: vizCatSystem, Label: "System prompts"},
	{ID: vizCatTools, Label: "Tool schemas"},
	{ID: vizCatSkills, Label: "Skill reads"},
	{ID: vizCatUser, Label: "User messages"},
	{ID: vizCatAssistant, Label: "Assistant replies"},
	{ID: vizCatThinking, Label: "Thinking"},
	{ID: vizCatToolCall, Label: "Tool calls"},
	{ID: vizCatToolResult, Label: "Tool results"},
	{ID: vizCatOverhead, Label: "Unattributed"},
}

// skillToolNames are the tools whose results are skill content rather than
// ordinary tool output, so their (often large) SKILL.md payloads classify
// separately from workaday tool results.
var skillToolNames = map[string]bool{
	"ListSkills": true,
	"ReadSkill":  true,
}

// vizEstimateTokens mirrors the engine's chars/4 heuristic
// (core.estimateTextTokens) so client-side estimates line up with the
// engine's contextTokens accounting.
func vizEstimateTokens(chars int) int64 {
	if chars == 0 {
		return 0
	}
	return int64((chars + 3) / 4)
}

// vizLabel derives a one-line label from a block of text.
func vizLabel(text string, max int) string {
	line := strings.TrimSpace(text)
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	runes := []rune(line)
	if len(runes) > max {
		line = string(runes[:max-1]) + "…"
	}
	if line == "" {
		line = "(empty)"
	}
	return line
}

// vizCacheMissSlack absorbs provider-side jitter in cache accounting (block
// boundaries, headers) so only genuine prefix invalidation flags as a miss.
const vizCacheMissSlack = 256

// vizDistribute spreads a measured token budget over the given items
// proportionally to their raw chars/4 estimates, marking them calibrated.
// The last item absorbs the rounding remainder so the group sums exactly to
// the budget. Returns any budget that could not be attributed (no items).
func vizDistribute(items []vizItem, idxs []int, budget int64) (unattributed int64) {
	if len(idxs) == 0 {
		return budget
	}
	if budget < 0 {
		budget = 0
	}
	var estSum int64
	for _, idx := range idxs {
		estSum += items[idx].Tokens
	}
	var allocated int64
	for j, idx := range idxs {
		var share int64
		switch {
		case j == len(idxs)-1:
			share = budget - allocated
		case estSum > 0:
			share = budget * items[idx].Tokens / estSum
		default:
			// All-empty blocks: split the budget evenly.
			share = budget / int64(len(idxs))
		}
		items[idx].Tokens = share
		items[idx].Est = false
		allocated += share
	}
	return 0
}

// buildContextVizSnapshot classifies a raw conversation into the snapshot the
// web UI renders. Pure: everything it needs is in the vizConversation.
//
// Token accounting happens in two passes. Classification first sizes every
// block with the engine's chars/4 heuristic. Then calibration reconciles
// those estimates with the provider-reported usage on each API call: call k
// occupies contextTokens_k, so the delta against call k-1 is the MEASURED
// size of everything that entered context in between — the response's own
// blocks get the call's exact outputTokens, and the prompt growth
// (delta - output) is spread over the between-call blocks (prompts, tool
// results) proportionally to their estimates. Only blocks after the last
// call keep raw estimates (matching how the engine computes contextTokens),
// so the classified total telescopes to the measured total and the
// "unattributed" bucket stays honest — and small.
func buildContextVizSnapshot(conv *vizConversation) *contextVizSnapshot {
	snap := &contextVizSnapshot{
		Agent:         conv.Agent,
		Model:         conv.Model,
		Provider:      conv.Provider,
		ContextTokens: conv.ContextTokens,
		AutoCompact:   conv.AutoCompact,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Skills:        conv.Skills,
		Calls:         []vizCall{},
	}
	if conv.ContextWindow != nil {
		snap.ContextWindow = *conv.ContextWindow
	}
	if conv.AutoCompact {
		snap.ReserveTokens = autoCompactReserveTokens
	}

	addItem := func(item vizItem) int {
		item.Index = len(snap.Items)
		item.Est = true
		snap.Items = append(snap.Items, item)
		return item.Index
	}

	// window collects the indexes of items not yet calibrated against a
	// measured API call: the fixed context, then everything added since the
	// last calibrated call.
	var window []int
	var prevContext int64 // occupied context as of the last calibrated call
	var unattributed int64

	// Tool schemas ride along on every API call, before any message.
	if conv.Tools != "" {
		toolCount := strings.Count(conv.Tools, "\n## ")
		if strings.HasPrefix(conv.Tools, "## ") {
			toolCount++
		}
		window = append(window, addItem(vizItem{
			Category: vizCatTools,
			Label:    fmt.Sprintf("Tool schemas (%d tools)", toolCount),
			Tokens:   vizEstimateTokens(len(conv.Tools)),
			Text:     conv.Tools,
			Fixed:    true,
		}))
	}

	callIdx := 0
	callTool := map[string]string{}
	var prevCache *vizUsage
	for _, msg := range conv.Messages {
		isCall := msg.Role == "ASSISTANT" && msg.TokenUsage.hasTokens()
		firstItem := len(snap.Items)
		var responseItems []int
		for _, block := range msg.Content {
			item := vizItem{
				Role: msg.Role,
				Kind: block.Kind,
			}
			switch block.Kind {
			case "TEXT":
				switch msg.Role {
				case "SYSTEM":
					item.Category = vizCatSystem
				case "ASSISTANT":
					item.Category = vizCatAssistant
				default:
					item.Category = vizCatUser
				}
				item.Label = vizLabel(block.Text, 120)
				item.Tokens = vizEstimateTokens(len(block.Text))
				item.Text = block.Text
			case "THINKING":
				item.Category = vizCatThinking
				item.Label = vizLabel(block.Text, 120)
				item.Tokens = vizEstimateTokens(len(block.Text))
				item.Text = block.Text
			case "TOOL_CALL":
				callTool[block.CallID] = block.ToolName
				item.Category = vizCatToolCall
				item.ToolName = block.ToolName
				item.Label = block.ToolName
				item.Tokens = vizEstimateTokens(len(block.ToolName) + len(block.Arguments))
				item.Text = block.Arguments
			case "TOOL_RESULT":
				toolName := callTool[block.CallID]
				item.ToolName = toolName
				if skillToolNames[toolName] {
					item.Category = vizCatSkills
				} else {
					item.Category = vizCatToolResult
				}
				if toolName == "" {
					toolName = "tool"
				}
				item.Label = toolName + " → " + vizLabel(block.Text, 80)
				item.Tokens = vizEstimateTokens(len(block.Text))
				item.Text = block.Text
				item.Errored = block.Errored
			default:
				// Future block kinds (images, audio): keep them visible
				// rather than silently dropping context.
				item.Category = vizCatOverhead
				item.Label = block.Kind
				item.Tokens = vizEstimateTokens(len(block.Text) + len(block.Arguments))
				item.Text = block.Text
			}
			if isCall {
				item.Call = callIdx + 1
			}
			idx := addItem(item)
			if isCall {
				responseItems = append(responseItems, idx)
			} else {
				window = append(window, idx)
			}
		}
		if !isCall {
			continue
		}

		callIdx++
		call := vizCall{
			Index:       callIdx,
			Item:        firstItem,
			Input:       msg.TokenUsage.InputTokens,
			Output:      msg.TokenUsage.OutputTokens,
			CacheReads:  msg.TokenUsage.CachedTokenReads,
			CacheWrites: msg.TokenUsage.CachedTokenWrites,
		}
		// A warm cache means this call reads at least what the previous
		// call had cached (reads + writes). Reading less means part of
		// the cached prefix was invalidated: a cache miss.
		if prevCache != nil && msg.TokenUsage.hasCacheActivity() {
			expected := prevCache.CachedTokenReads + prevCache.CachedTokenWrites
			if expected > 0 && call.CacheReads+vizCacheMissSlack < expected {
				call.CacheMiss = true
			}
		}
		if msg.TokenUsage.hasCacheActivity() {
			usage := msg.TokenUsage
			prevCache = &usage
		}
		snap.Calls = append(snap.Calls, call)

		// Calibrate: this call measured the occupied context, so the growth
		// since the last calibrated call is the measured size of the window
		// plus this response. The response's own blocks are measured exactly
		// (outputTokens); the rest of the growth is the prompt-side content.
		occupied := msg.TokenUsage.contextTokens()
		delta := occupied - prevContext
		if delta <= 0 {
			// The context did not measurably grow (unusual: e.g. restored
			// history with partial usage). Keep this window's raw estimates
			// and let a later call's delta cover it.
			window = append(window, responseItems...)
			continue
		}
		prevContext = occupied
		outBudget := min(msg.TokenUsage.OutputTokens, delta)
		unattributed += vizDistribute(snap.Items, window, delta-outBudget)
		unattributed += vizDistribute(snap.Items, responseItems, outBudget)
		window = window[:0]
	}

	// Total per category from the (now calibrated) items.
	totals := map[string]int64{}
	for _, item := range snap.Items {
		totals[item.Category] += item.Tokens
	}
	totals[vizCatOverhead] += unattributed
	for _, cat := range vizCategoryOrder {
		snap.ClassifiedTokens += totals[cat.ID]
	}
	// After calibration the classified total telescopes to the measured
	// context (last call's usage + estimates for trailing blocks, exactly
	// how the engine computes contextTokens), so any remaining gap is
	// genuine unattributed overhead -- e.g. provider-side wrapping the
	// usage numbers report but no block accounts for. Keep it visible
	// rather than hiding it.
	if gap := int64(conv.ContextTokens) - snap.ClassifiedTokens; gap > 0 {
		totals[vizCatOverhead] += gap
		snap.ClassifiedTokens += gap
	}
	for _, cat := range vizCategoryOrder {
		cat.Tokens = totals[cat.ID]
		snap.Categories = append(snap.Categories, cat)
	}

	return snap
}

// contextVizLLMID resolves the conversation state to visualize: the agent
// runtime's last committed snapshot when one exists (fresh at every step
// boundary, even mid-turn), falling back to the conversation's own LLM value.
func (a *sessionAgent) contextVizLLMID(ctx context.Context) (dagger.ID, error) {
	if rt := a.runtime(); rt != nil {
		if id, err := rt.SnapshotID(ctx); err == nil {
			return id, nil
		}
	}
	if a.llm == nil {
		return "", fmt.Errorf("no conversation yet")
	}
	return a.llm.ID(ctx)
}

// fetchContextVizConversation reads the focused conversation's raw snapshot
// from the engine.
func (s *LLMSession) fetchContextVizConversation(ctx context.Context) (*vizConversation, error) {
	// The visualizer is a pure observer: it re-reads the entire (ever-growing)
	// conversation on every poll, and the conversation's ID chain changes
	// every turn, so its telemetry would grow quadratically with conversation
	// length — enough to OOM the engine's telemetry stores on long sessions.
	// Opt every poll out of engine-side telemetry entirely.
	ctx = engine.ContextWithTelemetrySuppression(ctx)
	target := s.Target()
	if target == nil {
		return nil, fmt.Errorf("no conversation is focused")
	}
	llmID, err := target.contextVizLLMID(ctx)
	if err != nil {
		return nil, err
	}
	var res struct {
		Node vizConversation `json:"node"`
	}
	err = s.dag.Do(ctx, &dagger.Request{
		Query:     contextVizQuery,
		OpName:    "ContextViz",
		Variables: map[string]any{"id": llmID},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return nil, err
	}
	conv := res.Node
	conv.Agent = target.Name()
	conv.AutoCompact = target.ShouldAutocompact()
	return &conv, nil
}

// contextVizMux routes the visualizer's endpoints: the embedded page at /,
// and the live snapshot at /snapshot.json.
func contextVizMux(snapshot func(context.Context) (*contextVizSnapshot, error)) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(contextVizHTML) //nolint:errcheck
	})
	mux.HandleFunc("/snapshot.json", func(w http.ResponseWriter, r *http.Request) {
		snap, err := snapshot(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(snap) //nolint:errcheck
	})
	return mux
}

// ContextVizURL starts the visualizer's local web server on first use and
// returns its URL. The server lives for the rest of the session, bound to
// localhost on a random port, and always renders the conversation the
// session currently focuses.
func (s *LLMSession) ContextVizURL() (string, error) {
	s.contextVizL.Lock()
	defer s.contextVizL.Unlock()
	if s.contextVizURL != "" {
		return s.contextVizURL, nil
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("context visualizer: %w", err)
	}
	srv := &http.Server{
		Handler: contextVizMux(func(context.Context) (*contextVizSnapshot, error) {
			// Engine round trips run on the session's plumbing context: a
			// closed browser tab must not strand a half-evaluated query, and
			// the server dies with the session anyway.
			conv, err := s.fetchContextVizConversation(s.plumbingCtx)
			if err != nil {
				return nil, err
			}
			return buildContextVizSnapshot(conv), nil
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go srv.Serve(lis) //nolint:errcheck
	go func() {
		<-s.plumbingCtx.Done()
		srv.Close()
	}()
	s.contextVizURL = fmt.Sprintf("http://%s/", lis.Addr())
	return s.contextVizURL, nil
}

// ShowContextViz makes the visualizer reachable: it starts the server if
// needed, surfaces the URL in the sidebar (the browser may be unreachable,
// e.g. over SSH), and tries to open the user's browser.
func (s *LLMSession) ShowContextViz() {
	url, err := s.ContextVizURL()
	if err != nil {
		slog.Error("failed to start context visualizer", "error", err)
		return
	}
	if s.frontend != nil {
		s.frontend.SetSidebarContent(idtui.SidebarSection{
			Title:   "Context",
			Content: url,
		})
	}
	// The browser helper shells out (xdg-open & co); its output must not
	// scribble over the TUI, and the printed URL is the fallback anyway.
	browser.Stdout = io.Discard
	browser.Stderr = io.Discard
	if err := browser.OpenURL(url); err != nil {
		slog.Debug("could not open browser for context visualizer", "error", err, "url", url)
	}
}
