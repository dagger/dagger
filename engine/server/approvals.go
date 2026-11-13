package server

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/session/prompt"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// approvalSpec is one access request an owner approves on behalf of an agent
// or module: what kind of access, to what, and the question to ask.
type approvalSpec struct {
	// Kind names what is approved, e.g. "git-push", "git-force-push" or
	// "git-read". Grants are scoped to it, and frontends may style by it.
	Kind string
	// Subject is the thing access is requested to, in the one canonical
	// spelling grants are keyed by (a remote, a remote and ref, ...), as
	// literal text escaped like Question: frontends display it.
	Subject string
	// Question is the question to ask, as literal text already escaped for
	// terminals (see literalText), without the trailing "?": the broker
	// appends who asks. Frontends may match its leading words, so keep each
	// kind's wording stable.
	Question string
	// Danger marks a destructive operation, e.g. a force-push.
	Danger bool
}

// approvalKey is what a session grant covers: one owner letting one module
// have one kind of access to one subject. The module is what the owner trusts
// or not, so the grant holds for every agent using that module (a chief and
// its workers share their tool modules) and across the module's functions and
// its initialization: neither the agent nor the function is part of the key.
// A request without a module has an empty field, which only matches requests
// equally without one.
type approvalKey struct {
	owner   string // owning client ID
	kind    string
	module  string // resolvedModuleLoadIdentity: source, pin and name
	subject string
}

type pendingApproval struct {
	done     chan struct{}
	decision prompt.ApprovalDecision
	err      error
}

// approvalGrants remembers an owner's grants for a session. It lives only in
// daggerSession, never in a result, recipe, or client prompt key: forever
// grants are the client's to remember (under a request's persistent key).
//
// Only session-long grants are remembered. "Allow once" holds for that
// request, a "no" is often a course correction, and a canceled or unavailable
// prompt is no decision at all: later attempts ask again. Concurrent requests
// with the same key still share the one prompt in flight, and its answer,
// whatever it is.
//
// "Once" bounds the requests the broker sees, not what the caller does with
// the access it grants: each kind's caller decides what one request covers.
// For git-read, one request authorizes one credentialed lookup of an agent's
// address (see AuthorizeGitRead), whose result keeps the owner's credentials
// for as long as it lives.
type approvalGrants struct {
	mu        sync.Mutex
	decisions map[approvalKey]*pendingApproval
}

func (g *approvalGrants) check(ctx context.Context, key approvalKey, ask func(context.Context) (prompt.ApprovalDecision, error)) (prompt.ApprovalDecision, error) {
	g.mu.Lock()
	if pending, ok := g.decisions[key]; ok {
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return prompt.APPROVAL_DENY, ctx.Err()
		case <-pending.done:
			return pending.decision, pending.err
		}
	}
	pending := &pendingApproval{done: make(chan struct{})}
	if g.decisions == nil {
		g.decisions = make(map[approvalKey]*pendingApproval)
	}
	g.decisions[key] = pending
	g.mu.Unlock()
	pending.decision, pending.err = ask(ctx)
	if pending.err != nil {
		pending.decision = prompt.APPROVAL_DENY
	}
	g.mu.Lock()
	if !approvalRemembered(pending.decision) {
		delete(g.decisions, key)
	}
	close(pending.done)
	g.mu.Unlock()
	return pending.decision, pending.err
}

func approvalAllows(decision prompt.ApprovalDecision) bool {
	switch decision {
	case prompt.APPROVAL_ALLOW_ONCE, prompt.APPROVAL_ALLOW_SESSION, prompt.APPROVAL_ALLOW_FOREVER:
		return true
	default:
		return false
	}
}

func approvalRemembered(decision prompt.ApprovalDecision) bool {
	return decision == prompt.APPROVAL_ALLOW_SESSION || decision == prompt.APPROVAL_ALLOW_FOREVER
}

var errApprovalNotInteractive = errors.New("owning client cannot prompt (not interactive)")

// requestApproval asks owner, the trusted client whose credentials or
// authority are at stake, to approve spec for the code running as client. It
// resolves who asks itself (see resolveApprovalRequester), so neither a module
// nor a model can claim to be someone else. A grant for the session is
// remembered there (see approvalGrants); a forever grant is the owner's
// client to remember, under approvalPersistentKey.
//
// It returns whether the request is allowed; an error means nobody decided
// (the prompt failed, was canceled, or the owner cannot prompt).
func (srv *Server) requestApproval(ctx context.Context, client, owner *clientRuntime, spec approvalSpec) (bool, error) {
	requester, err := resolveApprovalRequester(ctx, client)
	if err != nil {
		return false, err
	}
	key := approvalKey{
		owner:   owner.clientID,
		kind:    spec.Kind,
		module:  resolvedModuleLoadIdentity(requester.module),
		subject: spec.Subject,
	}
	req := &prompt.ApprovalRequest{
		Kind:          spec.Kind,
		Prompt:        approvalPrompt(spec.Question, requester),
		Subject:       spec.Subject,
		Requester:     requester.proto(),
		PersistentKey: approvalPersistentKey(spec.Kind, requester.module, spec.Subject),
		Danger:        spec.Danger,
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		req.TraceId = sc.TraceID().String()
		req.SpanId = sc.SpanID().String()
	}
	decision, err := client.daggerSession.approvals.check(ctx, key, func(ctx context.Context) (prompt.ApprovalDecision, error) {
		return srv.promptApproval(ctx, owner, req)
	})
	if err != nil {
		return false, err
	}
	return approvalAllows(decision), nil
}

// promptApproval asks owner's client. A client predating PromptApproval gets
// the same question as a yes/no prompt, where yes allows for the session, as
// it did before approvals had a grant scope.
func (srv *Server) promptApproval(ctx context.Context, owner *clientRuntime, req *prompt.ApprovalRequest) (prompt.ApprovalDecision, error) {
	conn, available, err := srv.SpecificClientAttachableConn(ctx, owner.clientID, core.SpecificClientAttachableConnOpts{IfAvailable: true})
	if err != nil {
		return prompt.APPROVAL_DENY, err
	}
	if !available {
		return prompt.APPROVAL_DENY, fmt.Errorf("owning client is not available to approve the request")
	}
	client := prompt.NewPromptClient(conn)
	response, err := client.PromptApproval(ctx, req)
	switch status.Code(err) {
	case codes.OK:
	case codes.Unimplemented:
		// Never send the persistent key here: the client would remember a
		// yes forever when it only grants for the session.
		response, err := client.PromptBool(ctx, &prompt.BoolRequest{Prompt: req.GetPrompt()})
		if status.Code(err) == codes.Unimplemented || status.Code(err) == codes.FailedPrecondition {
			return prompt.APPROVAL_DENY, errApprovalNotInteractive
		}
		if err != nil {
			return prompt.APPROVAL_DENY, err
		}
		if response.GetResponse() {
			return prompt.APPROVAL_ALLOW_SESSION, nil
		}
		return prompt.APPROVAL_DENY, nil
	case codes.FailedPrecondition:
		return prompt.APPROVAL_DENY, errApprovalNotInteractive
	default:
		return prompt.APPROVAL_DENY, err
	}
	switch decision := response.GetDecision(); decision {
	case prompt.APPROVAL_ALLOW_ONCE, prompt.APPROVAL_ALLOW_SESSION:
		return decision, nil
	case prompt.APPROVAL_ALLOW_FOREVER:
		if req.GetPersistentKey() == "" {
			// Not offered: never widen a grant past what was asked for.
			return prompt.APPROVAL_ALLOW_SESSION, nil
		}
		return decision, nil
	default:
		// A denial, or a decision this engine does not know.
		return prompt.APPROVAL_DENY, nil
	}
}

// approvalRequester is who asks for approval, as the engine resolved it. Only
// the module scopes a grant; the agent and function are for display,
// so the owner sees who is asking, and frontends can show the request outside
// the conversation (a roster, a notification).
type approvalRequester struct {
	agentName, agentHandle string
	// module is the module nearest the caller: the one whose code drives the
	// request. Zero when no module is involved.
	module dagql.ObjectResult[*core.Module]
	// function is the module function that made the request, e.g.
	// "Staff.spawn"; best effort, empty when unknown.
	function string
}

// resolveApprovalRequester resolves who asks for approval from the engine's
// own records, never from anything the caller passes: the agent from the
// call's context (core.CallerAgent), the module nearest the caller, and the
// function that module client is running. A module container's nested API
// client has no function call of its own, so its agent is the one stamped on
// the nearest module's function call, which the prompt names.
func resolveApprovalRequester(ctx context.Context, client *clientRuntime) (approvalRequester, error) {
	var r approvalRequester
	modClient, err := nearestModuleClient(client)
	if err != nil {
		return r, err
	}
	agent, ok := core.CallerAgent(ctx)
	if !ok && modClient != nil {
		agent, ok = modClient.fnCall.CallerAgent()
	}
	if ok {
		r.agentName = agent.Self().Name
		r.agentHandle = agent.Self().Handle
	}
	if modClient != nil {
		r.module = modClient.mod
		if fnCall := modClient.fnCall; fnCall != nil && fnCall.Name != "" {
			r.function = fnCall.Name
			if fnCall.ParentName != "" {
				r.function = fnCall.ParentName + "." + fnCall.Name
			}
		}
	}
	return r, nil
}

// nearestModuleClient returns the module client nearest the caller (the
// caller itself, or the closest ancestor serving a module): the module whose
// code drives the request, even from a container's nested API client. Nil
// when no module is involved.
func nearestModuleClient(client *clientRuntime) (*clientRuntime, error) {
	if client.mod.Self() != nil {
		return client, nil
	}
	parents, err := client.daggerSession.ancestorRuntimes(client.clientRecord)
	if err != nil {
		return nil, err
	}
	for i := len(parents) - 1; i >= 0; i-- {
		if parents[i].mod.Self() != nil {
			return parents[i], nil
		}
	}
	return nil, nil
}

// proto is r as sent to the frontend. The engine resolved every value, but the
// names are chosen by a module or by the code spawning the agent, so they are
// literal text escaped for terminals, like the subject and the prompt. The
// handle is the engine's own identifier, which frontends match as is.
func (r approvalRequester) proto() *prompt.ApprovalRequester {
	p := &prompt.ApprovalRequester{
		AgentName:   literalText(r.agentName),
		AgentHandle: r.agentHandle,
		Function:    literalText(r.function),
	}
	if mod := r.module.Self(); mod != nil {
		p.Module = literalText(mod.Name())
		if src := mod.GetSource(); src != nil {
			ref := canonicalModuleReference(src)
			if src.Kind == core.ModuleSourceKindGit && src.Pin() != "" {
				subpath := src.SourceSubpath
				if subpath == "" {
					subpath = src.SourceRootSubpath
				}
				ref = core.GitRefString(src.Git.CloneRef, subpath, src.Pin())
			}
			p.ModuleRef = literalText(ref)
		}
	}
	return p
}

// approvalPrompt is spec's question naming who asks, for frontends that show
// only the text (the yes/no fallback). Every value is literal text, escaped
// for terminals (including bidi/control characters), not Markdown: agent and
// module names are chosen by the code asking.
func approvalPrompt(question string, r approvalRequester) string {
	var by []string
	agent := r.agentName
	if agent == "" {
		agent = r.agentHandle
	}
	if agent != "" {
		by = append(by, "agent "+strconv.QuoteToASCII(agent))
	}
	if mod := r.module.Self(); mod != nil {
		by = append(by, "module "+strconv.QuoteToASCII(mod.Name()))
	}
	if r.function != "" {
		by = append(by, "function "+strconv.QuoteToASCII(r.function))
	}
	if len(by) == 0 {
		return question + "?"
	}
	return question + " (requested by " + strings.Join(by, ", ") + ")?"
}

// approvalPersistentKey is the identity a forever ("always allow <module>")
// grant is remembered under by the owner's client, or "" to not offer
// forever.
//
// Like a session grant, it covers one module's one kind of access to one
// subject, for every agent using that module: no agent is part of it. But it
// outlives the session, so it cannot use the module's load identity, whose pin
// moves with every update. It is keyed instead by the kind, the exact subject,
// and the module's git source without its pin (its clone URL and subpath) and
// its name, so an update of the same module keeps the grant, while another
// source does not.
//
// Only modules loaded from git are identified well enough to remember. A
// local module is only a path, which any module loaded from the same path
// (another checkout, or a foreign module put there) would share; a module
// built from a directory has no source the engine can name; and a request
// without a module has nothing to remember. Those are only offered up to a
// session grant.
//
// Every field is quoted, so no value can spell another's separator. Bump the
// version when the identity changes, so old grants are asked again rather
// than reinterpreted: v1 keys also named the agent, and never match.
func approvalPersistentKey(kind string, module dagql.ObjectResult[*core.Module], subject string) string {
	mod := module.Self()
	if mod == nil {
		return ""
	}
	src := mod.GetSource()
	if src == nil || src.Kind != core.ModuleSourceKindGit {
		return ""
	}
	ref := canonicalModuleReference(src)
	if ref == "" {
		return ""
	}
	return "approval:v2:" + strings.Join([]string{
		strconv.Quote(kind),
		strconv.Quote(ref + "|" + canonicalWorkspaceModuleName(mod.Name())),
		strconv.Quote(subject),
	}, ":")
}
