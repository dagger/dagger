package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/opencontainers/go-digest"
)

// stepHarness runs synchronous LLM.step through the same persistent adapter
// machinery as Agent. The temporary runtime lives for one native turn; its
// pending user suffix is already materialized on inst, so checkpoint commit
// appends only native output and state.
func (llm *LLM) stepHarness(ctx context.Context, inst dagql.ObjectResult[*LLM], maxTokens int) (dagql.ObjectResult[*LLM], error) {
	if !llm.HasPending() {
		return inst, nil
	}

	rt := &AgentRuntime{
		last:         inst,
		messages:     map[string]*agentMessageRecord{},
		stateChanged: make(chan struct{}),
	}
	messageID := identity.NewID()
	rt.messages[messageID] = &agentMessageRecord{
		harnessMaterialized: true,
	}

	runtime, unregister, err := rt.startHarness(ctx, inst, maxTokens)
	if err != nil {
		return inst, err
	}
	defer unregister()
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), TerminateGracePeriod)
		defer cancel()
		_ = runtime.Close(cleanupCtx)
	}()

	pending := pendingHarnessMessages(llm.Messages)
	if err := runtime.Enqueue(ctx, messageID, pending); err != nil {
		return inst, err
	}
	_, awaitErr := runtime.Await(ctx, messageID)
	result := rt.Snapshot()
	if awaitErr != nil {
		return result, awaitErr
	}
	return result, nil
}

func pendingHarnessMessages(messages []*LLMMessage) []*LLMMessage {
	start := len(messages) - 1
	for start > 0 && messages[start-1].Role == LLMMessageRoleUser {
		start--
	}
	return cloneLLMMessages(messages[start:])
}

// runHarness replaces provider Step for a harness-backed Agent loop. One
// process, adapter, correlation ledger, and dispatcher remain hot until the
// Agent loop exits.
func (rt *AgentRuntime) runHarness(ctx context.Context) error {
	runtime, unregister, err := rt.startHarness(ctx, rt.last, 0)
	if err != nil {
		return err
	}
	rt.mu.Lock()
	rt.harness = runtime
	rt.mu.Unlock()
	defer func() {
		_ = runtime.Close(context.WithoutCancel(ctx))
		unregister()
		rt.mu.Lock()
		rt.harness = nil
		rt.mu.Unlock()
	}()

	for {
		rt.mu.Lock()
		if rt.stopRequested || ctx.Err() != nil {
			rt.mu.Unlock()
			return nil
		}
		if rt.paused {
			rt.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil
			case <-rt.wake:
			}
			continue
		}
		var submissions []struct {
			id   string
			rec  *agentMessageRecord
			text string
		}
		for _, id := range rt.mailbox {
			rec := rt.messages[id]
			if rec == nil || rec.harnessSubmitted {
				continue
			}
			rec.harnessSubmitted = true
			rt.harnessActive++
			submissions = append(submissions, struct {
				id   string
				rec  *agentMessageRecord
				text string
			}{id: id, rec: rec, text: rec.text})
		}
		if len(submissions) > 0 {
			rt.transitionLocked(func() { rt.stepping = true })
		}
		rt.mu.Unlock()

		for _, submission := range submissions {
			content := []*LLMMessage{{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: submission.text}}}}
			if err := runtime.Enqueue(ctx, submission.id, content); err != nil {
				return err
			}
			go rt.watchHarnessMessage(ctx, runtime, submission.id, submission.rec)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-rt.wake:
		}
	}
}

func (rt *AgentRuntime) watchHarnessMessage(ctx context.Context, runtime *LLMHarnessRuntime, id string, rec *agentMessageRecord) {
	delivery, deliveryErr := runtime.Delivery(ctx, id)
	rt.mu.Lock()
	if deliveryErr == nil {
		if len(rt.mailbox) == 0 || rt.mailbox[0] != id {
			deliveryErr = fmt.Errorf("harness consumed Agent message %q outside FIFO order", id)
		} else {
			rt.mailbox = rt.mailbox[1:]
			rec.consumed = true
			rt.consumed = append(rt.consumed, rec)
			rt.turnOpen = true
		}
	} else if len(rt.mailbox) > 0 && rt.mailbox[0] == id {
		// Correlated cancellation/refusal is also definitive evidence for the
		// current FIFO head. Advance it even though it never joined a turn.
		rt.mailbox = rt.mailbox[1:]
	}
	rt.finalizeDeliveryLocked(rec, delivery, deliveryErr)
	if deliveryErr != nil {
		rt.resolveLocked(rec, "", deliveryErr)
		rt.harnessActive--
		if rt.harnessActive == 0 {
			rt.stepping = false
		}
		rt.transitionLocked(func() {})
		rt.mu.Unlock()
		rt.pokeWake()
		return
	}
	rt.transitionLocked(func() {})
	rt.mu.Unlock()

	reply, awaitErr := runtime.Await(ctx, id)
	rt.mu.Lock()
	rt.transitionLocked(func() {
		rt.resolveLocked(rec, reply, awaitErr)
		for index, consumed := range rt.consumed {
			if consumed == rec {
				rt.consumed = append(rt.consumed[:index], rt.consumed[index+1:]...)
				break
			}
		}
		rt.harnessActive--
		if rt.harnessActive == 0 {
			rt.stepping = false
			rt.turnOpen = false
		}
	})
	rt.mu.Unlock()
	rt.pokeWake()
}

func (rt *AgentRuntime) pokeWake() {
	select {
	case rt.wake <- struct{}{}:
	default:
	}
}

func (rt *AgentRuntime) startHarness(ctx context.Context, inst dagql.ObjectResult[*LLM], maxTokens int) (*LLMHarnessRuntime, func(), error) {
	llm := inst.Self()
	callDigest := ""
	if dig, err := inst.RecipeDigest(ctx); err == nil {
		callDigest = dig.String()
	}
	display := newLLMHarnessDisplay(ctx, callDigest)
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, nil, err
	}
	registry, err := query.ExecHTTPHandlers(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get harness HTTP registry: %w", err)
	}
	mcp := llm.mcp
	if len(mcp.boundTools) == 0 {
		mcp, err = mcp.bindWorkspaceModuleTools(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("bind harness workspace module tools: %w", err)
		}
	}
	toolServer, err := NewLLMToolServer(ctx, mcp)
	if err != nil {
		return nil, nil, err
	}
	toolServer.withCallContext(display.mcpCallContext)
	httpHandler := toolServer.StreamableHTTPHandler()
	var execToken string
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+execToken {
			http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		httpHandler.ServeHTTP(response, request)
	})
	execToken, unregister := registry.Register(handler)
	if execToken == "" {
		return nil, nil, fmt.Errorf("register harness HTTP handler: session is closing")
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		unregister()
		return nil, nil, err
	}
	workspace, err := mcp.workspaceDirectory(ctx, srv)
	if err != nil {
		unregister()
		return nil, nil, fmt.Errorf("resolve harness workspace: %w", err)
	}
	checkpoint := llm.harnessCheckpoint.clone()
	if !checkpoint.validFor(llm.Messages, llm.harnessKind) {
		checkpoint = nil
	}
	var nativeSession string
	if llm.harnessKind == LLMHarnessClaude && checkpoint != nil && checkpoint.Protocol == claudeHarnessProtocol {
		nativeSession = checkpoint.NativeSession
	}
	auth, err := llmHarnessAuthOffer(ctx, query, llm.harnessKind)
	if err != nil {
		unregister()
		return nil, nil, fmt.Errorf("resolve %s harness auth: %w", llm.harnessKind, err)
	}
	process, err := startLLMHarnessProcess(ctx, llm.harness, llm.harnessKind, workspace, execToken, nativeSession)
	if err != nil {
		unregister()
		return nil, nil, err
	}
	go func() { _, _ = io.Copy(io.Discard, process.Stderr()) }()

	var adapter LLMHarnessAdapter
	switch llm.harnessKind {
	case LLMHarnessCodex:
		adapter = NewCodexLLMHarnessAdapter(process)
	case LLMHarnessClaude:
		adapter = NewClaudeLLMHarnessAdapter(process)
	default:
		_ = process.Stop(context.WithoutCancel(ctx))
		unregister()
		return nil, nil, fmt.Errorf("unsupported LLM harness kind %q", llm.harnessKind)
	}
	start := LLMHarnessStart{
		History:    cloneLLMMessages(llm.Messages),
		Checkpoint: checkpoint,
		Model:      llmHarnessNativeModel(llm.harnessKind, llm.model),
		MaxTokens:  maxTokens,
		// The execution listener publishes its selected port through this env.
		// Vendor adapters which consume MCPURL may substitute it in their native
		// configuration; the forwarding path itself is selected by execToken.
		MCPURL:     "http://127.0.0.1:${" + engineutil.DaggerSessionPortEnv + "}" + engineutil.DaggerExecHTTPPath,
		MCPToken:   execToken,
		CallDigest: callDigest,
		Auth:       auth,
		display:    display,
	}
	runtime, err := NewLLMHarnessRuntime(ctx, llm.harnessKind, adapter, start, func(commitCtx context.Context, commit LLMHarnessCommit) (string, error) {
		return rt.commitHarnessTurn(commitCtx, commit)
	})
	if err != nil {
		display.close(err)
		_ = process.Stop(context.WithoutCancel(ctx))
		unregister()
		return nil, nil, err
	}
	return runtime, unregister, nil
}

func llmHarnessNativeModel(kind LLMHarnessKind, model string) string {
	if kind == LLMHarnessCodex {
		return strings.TrimPrefix(model, codexModelPrefix)
	}
	return model
}

// llmHarnessAuthOffer treats the public harness kind as the explicit trust
// boundary: selecting an official CLI protocol authorizes Core to obtain only
// that harness's matching session credential. The credential is passed directly
// to the adapter at runtime and never becomes an SDK-visible Secret or part of
// the caller-supplied container.
func llmHarnessAuthOffer(ctx context.Context, query *Query, kind LLMHarnessKind) (*LLMHarnessAuth, error) {
	if kind != LLMHarnessCodex {
		// Claude Code accepts API keys and CLAUDE_CODE_OAUTH_TOKEN before its
		// stream protocol starts, but has no equivalent refresh callback. Its
		// runtime-only process injection belongs here once that adapter supports
		// it; keep the normalized auth contract vendor-neutral in the meantime.
		return nil, nil
	}
	router, err := loadLLMRouter(ctx, query)
	if err != nil {
		return nil, err
	}
	return codexHarnessAuthOffer(router), nil
}

func codexHarnessAuthOffer(router *LLMRouter) *LLMHarnessAuth {
	if router == nil {
		return nil
	}
	// Prefer the subscription credential. Codex app-server's external-auth
	// protocol keeps refresh ownership with Dagger and never writes a refresh
	// token into the harness filesystem.
	if router.reloadCodexAuthToken != nil && router.forceReloadCodexAuthToken != nil && extractChatGPTAccountID(router.OpenAICodexAuthToken) != "" {
		normal := router.reloadCodexAuthToken
		forced := router.forceReloadCodexAuthToken
		return &LLMHarnessAuth{Kind: LLMHarnessAuthOAuth, Resolve: func(resolveCtx context.Context, force bool) (LLMHarnessAuthState, error) {
			resolve := normal
			if force {
				resolve = forced
			}
			credential, err := resolve(resolveCtx)
			if err != nil {
				return LLMHarnessAuthState{}, err
			}
			accountID, planType := extractChatGPTAuthClaims(credential.Token)
			if credential.Token == "" || accountID == "" {
				return LLMHarnessAuthState{}, fmt.Errorf("openai-codex OAuth credential omitted access token or account ID")
			}
			return LLMHarnessAuthState{
				Token:     credential.Token,
				AccountID: accountID,
				PlanType:  planType,
			}, nil
		}}
	}
	// API-key users use app-server's official login RPC too. This preserves the
	// installable module's fallback without resolving a Secret in module code or
	// adding the key to the cold container recipe.
	if router.OpenAIAPIKey != "" {
		key := router.OpenAIAPIKey
		return &LLMHarnessAuth{Kind: LLMHarnessAuthAPIKey, Resolve: func(context.Context, bool) (LLMHarnessAuthState, error) {
			return LLMHarnessAuthState{Token: key}, nil
		}}
	}
	return nil
}

func (rt *AgentRuntime) commitHarnessTurn(ctx context.Context, commit LLMHarnessCommit) (string, error) {
	rt.mu.Lock()
	base := rt.last
	texts := make([]string, 0, len(commit.DaggerMessageIDs))
	for _, id := range commit.DaggerMessageIDs {
		rec := rt.messages[id]
		if rec == nil {
			rt.mu.Unlock()
			return "", fmt.Errorf("commit harness turn: unknown Agent message %q", id)
		}
		if !rec.harnessMaterialized {
			texts = append(texts, rec.text)
		}
	}
	rt.mu.Unlock()

	selectors := make([]dagql.Selector, 0, len(texts)+len(commit.Messages))
	for _, text := range texts {
		selectors = append(selectors, dagql.Selector{Field: "withPrompt", Args: []dagql.NamedInput{{Name: "prompt", Value: dagql.NewString(text)}}})
	}
	for _, message := range commit.Messages {
		switch message.Role {
		case LLMMessageRoleAssistant:
			usage := LLMTokenUsage{}
			if message.TokenUsage != nil {
				usage = *message.TokenUsage
			}
			selector, err := responseSelector(&LLMResponse{Content: message.Content, TokenUsage: usage})
			if err != nil {
				return "", err
			}
			selectors = append(selectors, selector)
		case LLMMessageRoleUser:
			for _, block := range message.Content {
				if block.Kind != LLMContentToolResult {
					continue
				}
				selectors = append(selectors, dagql.Selector{
					Field: "withToolResult",
					Args: []dagql.NamedInput{
						{Name: "callId", Value: dagql.NewString(block.CallID)},
						{Name: "content", Value: dagql.NewString(block.Text)},
						{Name: "errored", Value: dagql.NewBoolean(block.Errored)},
					},
				})
			}
		}
	}
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return "", err
	}
	materialized := base
	if len(selectors) > 0 {
		if err := srv.Select(ctx, base, &materialized, selectors...); err != nil {
			return "", err
		}
	}
	historyDigest, err := llmHarnessHistoryDigest(materialized.Self().Messages)
	if err != nil {
		return "", err
	}
	correlationsJSON, err := json.Marshal(commit.Correlations)
	if err != nil {
		return "", fmt.Errorf("marshal LLM harness correlations: %w", err)
	}
	var next dagql.ObjectResult[*LLM]
	if err := srv.Select(ctx, materialized, &next, dagql.Selector{
		Field: "__withHarnessCheckpoint",
		Args: []dagql.NamedInput{
			{Name: "messageCount", Value: dagql.NewInt(len(materialized.Self().Messages))},
			{Name: "historyDigest", Value: dagql.NewString(historyDigest.String())},
			{Name: "nativeSession", Value: dagql.NewString(commit.NativeState.NativeSession)},
			{Name: "protocol", Value: dagql.NewString(commit.NativeState.Protocol)},
			{Name: "correlations", Value: dagql.NewDigestedSerializedString(commit.Correlations, digest.FromBytes(correlationsJSON))},
		},
	}); err != nil {
		return "", err
	}
	rt.mu.Lock()
	rt.transitionLocked(func() { rt.commitLast(ctx, next) })
	rt.mu.Unlock()
	reply, _ := next.Self().LastReply()
	return strings.TrimSpace(reply), nil
}
