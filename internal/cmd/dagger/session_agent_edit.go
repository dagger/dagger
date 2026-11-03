package daggercmd

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	telemetry "github.com/dagger/otel-go"
)

// agentBoundaryEditor is private CLI plumbing, not a generated/public Agent API.
// The engine holds a step/drain boundary without canceling the active turn or
// consuming mailbox messages. Releasing the edit preserves its prior run state.
type agentBoundaryEditor interface {
	BeginEdit(context.Context, string) (core.ID, error)
	CommitEdit(context.Context, string, *core.LLM) error
	AbortEdit(context.Context, string) error
}

func (l liveAgent) privateEdit(ctx context.Context, field, token string, conversation *core.LLM) (core.ID, error) {
	agentID, err := l.agent.ID(ctx)
	if err != nil {
		return "", err
	}
	variables := map[string]any{"id": agentID, "token": token}
	var extraVariable, extraArgument string
	if conversation != nil {
		id, err := conversation.ID(ctx)
		if err != nil {
			return "", err
		}
		variables["conversation"] = id
		extraVariable = ", $conversation: ID!"
		extraArgument = ", conversation: $conversation"
	}
	selection := ""
	if field == "__beginEdit" {
		selection = " { id }"
	}
	var response struct {
		Node struct {
			Edit json.RawMessage
		}
	}
	err = l.dag.Do(ctx, &dagger.Request{
		Query: fmt.Sprintf(`query($id: ID!, $token: String!%s) {
			node(id: $id) { ... on Agent { edit: %s(token: $token%s)%s } }
		}`, extraVariable, field, extraArgument, selection),
		Variables: variables,
	}, &dagger.Response{Data: &response})
	if err != nil {
		return "", err
	}
	if field == "__beginEdit" {
		var result struct{ ID core.ID }
		err = json.Unmarshal(response.Node.Edit, &result)
		return result.ID, err
	}
	var id core.ID
	err = json.Unmarshal(response.Node.Edit, &id)
	return id, err
}

func (l liveAgent) BeginEdit(ctx context.Context, token string) (core.ID, error) {
	return l.privateEdit(ctx, "__beginEdit", token, nil)
}

func (l liveAgent) CommitEdit(ctx context.Context, token string, conversation *core.LLM) error {
	_, err := l.privateEdit(ctx, "__commitEdit", token, conversation)
	return err
}

func (l liveAgent) AbortEdit(ctx context.Context, token string) error {
	_, err := l.privateEdit(ctx, "__abortEdit", token, nil)
	return err
}

// lockConversation serializes prompt preparation, boundary edits, and snapshot
// adoption, but never the wait for a prompt's response. In particular, an edit
// cannot replace a local seed while WithPromptInput is preparing/spawning it, and
// a response waiter must fetch its snapshot after any concurrent edit commits.
func (a *sessionAgent) lockConversation(ctx context.Context) (func(), error) {
	a.mutationOnce.Do(func() { a.mutationGate = make(chan struct{}, 1) })
	select {
	case a.mutationGate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-a.mutationGate
			return nil, context.Cause(ctx)
		}
		return func() { <-a.mutationGate }, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func (a *sessionAgent) llmSnapshot() (*core.LLM, modelSelection) {
	a.llmL.RLock()
	defer a.llmL.RUnlock()
	return a.llm, modelSelection{Provider: a.provider, Model: a.model}
}

func resolveCommandModel(ctx context.Context, llm *core.LLM) (modelSelection, error) {
	if llm == nil {
		return modelSelection{}, errors.New("agent has no conversation to edit")
	}
	model, err := llm.Model(ctx)
	if err != nil {
		return modelSelection{}, err
	}
	provider, err := llm.Provider(ctx)
	if err != nil {
		return modelSelection{}, err
	}
	return modelSelection{Provider: provider, Model: model}, nil
}

// editAtBoundary never interrupts, resumes, sends a prompt, or falls back to
// detaching the runtime. A rejected/canceled edit leaves the original turn alone.
func (a *sessionAgent) editAtBoundary(ctx context.Context, operation string, edit func(context.Context, *core.LLM) (*core.LLM, error)) (rerr error) {
	ctx, span := Tracer().Start(ctx, operation, telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)
	unlock, err := a.lockConversation(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	base, _ := a.llmSnapshot()
	var editor agentBoundaryEditor
	var token string
	committed := false
	if rt := a.runtime(); rt != nil {
		var ok bool
		editor, ok = rt.(agentBoundaryEditor)
		if !ok {
			return errors.New("this engine does not support edits at agent step boundaries")
		}
		token = rand.Text()
		// Begin may have acquired the barrier even if its response was canceled.
		// Always release using the caller-generated token on every failure path.
		defer func() {
			if committed {
				return
			}
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if err := editor.AbortEdit(cleanupCtx, token); err != nil {
				rerr = errors.Join(rerr, fmt.Errorf("release pending agent edit: %w", err))
			}
		}()
		id, err := func() (_ core.ID, rerr error) {
			waitCtx, waitSpan := Tracer().Start(ctx, "waiting for next agent step boundary")
			defer telemetry.EndWithCause(waitSpan, &rerr)
			return editor.BeginEdit(waitCtx, token)
		}()
		if err != nil {
			return err
		}
		if id == "" {
			return errors.New("agent edit returned no conversation snapshot")
		}
		base = core.Ref[*core.LLM](core.NewQuery(a.session.dag), id)
	}
	if base == nil {
		return errors.New("agent has no conversation to edit")
	}
	next, err := edit(ctx, base)
	if err != nil {
		return err
	}
	model, err := resolveCommandModel(ctx, next)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	if editor != nil {
		if err := editor.CommitEdit(ctx, token, next); err != nil {
			return err
		}
		committed = true
	}
	a.adoptLLM(next, model)
	return nil
}

// withSettledRuntime guards explicit wholesale replacements after settling the
// old turn. Settling must precede the gate: the canceled response waiter needs
// that same gate to synchronize its final snapshot before the turn can end.
func (a *sessionAgent) withSettledRuntime(ctx context.Context, replace func(agentRuntime) error) error {
	if _, err := a.settleTurn(ctx); err != nil {
		return err
	}
	unlock, err := a.lockConversation(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return replace(a.runtime())
}

// StopForLogout is deliberately stronger than dropping an attachment: the user
// confirmed stopping the focused runtime, including one owned by another agent.
// Keep its tombstone/identity; do not silently fork a replacement conversation.
func (a *sessionAgent) StopForLogout(ctx context.Context) (rerr error) {
	ctx, span := Tracer().Start(ctx, "stopping agent before logout", telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)
	if _, err := a.settleTurn(ctx); err != nil {
		return err
	}
	unlock, err := a.lockConversation(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if rt := a.runtime(); rt != nil {
		return rt.Stop(ctx)
	}
	return nil
}
