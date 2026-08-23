package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

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
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, nil, err
	}
	registry, err := query.ExecHTTPHandlers(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get harness HTTP registry: %w", err)
	}
	toolServer, err := NewLLMToolServer(ctx, llm.mcp)
	if err != nil {
		return nil, nil, err
	}
	httpHandler := toolServer.StreamableHTTPHandler()
	bearer := identity.NewID()
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+bearer {
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
	workspace, err := llm.mcp.workspaceDirectory(ctx, srv)
	if err != nil {
		unregister()
		return nil, nil, fmt.Errorf("resolve harness workspace: %w", err)
	}
	process, err := startLLMHarnessProcess(ctx, llm.harness, llm.harnessKind, workspace, execToken)
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
	callDigest := ""
	if dig, err := inst.RecipeDigest(ctx); err == nil {
		callDigest = dig.String()
	}
	start := LLMHarnessStart{
		History:    cloneLLMMessages(llm.Messages),
		Checkpoint: llm.harnessCheckpoint.clone(),
		Model:      llm.model,
		MaxTokens:  maxTokens,
		// The execution listener publishes its selected port through this env.
		// Vendor adapters which consume MCPURL may substitute it in their native
		// configuration; the forwarding path itself is selected by execToken.
		MCPURL:     "http://127.0.0.1:${" + engineutil.DaggerSessionPortEnv + "}" + engineutil.DaggerExecHTTPPath,
		MCPToken:   bearer,
		CallDigest: callDigest,
	}
	runtime, err := NewLLMHarnessRuntime(ctx, llm.harnessKind, adapter, start, func(commitCtx context.Context, commit LLMHarnessCommit) (string, error) {
		return rt.commitHarnessTurn(commitCtx, commit)
	})
	if err != nil {
		_ = process.Stop(context.WithoutCancel(ctx))
		unregister()
		return nil, nil, err
	}
	return runtime, unregister, nil
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
		texts = append(texts, rec.text)
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
			selectors = append(selectors, toolResultSelectors(base.Self(), []*LLMMessage{message}, nil)...)
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
	checkpointed := materialized.Self().Clone()
	historyJSON, err := json.Marshal(checkpointed.Messages)
	if err != nil {
		return "", err
	}
	checkpointed.harnessCheckpoint = &LLMHarnessCheckpoint{
		Harness:       checkpointed.harness,
		Kind:          checkpointed.harnessKind,
		MessageCount:  len(checkpointed.Messages),
		HistoryDigest: digest.FromBytes(historyJSON),
		NativeSession: commit.NativeState.NativeSession,
		Protocol:      commit.NativeState.Protocol,
		Correlations:  append([]LLMHarnessMessageCorrelation(nil), commit.Correlations...),
	}
	next, err := dagql.NewObjectResultForCurrentCall(ctx, srv, checkpointed)
	if err != nil {
		return "", err
	}
	rt.mu.Lock()
	rt.transitionLocked(func() { rt.commitLast(ctx, next) })
	rt.mu.Unlock()
	reply, _ := checkpointed.LastReply()
	return strings.TrimSpace(reply), nil
}
