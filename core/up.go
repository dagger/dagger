package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/parallel"
	"github.com/vektah/gqlparser/v2/ast"
)

// Up represents a service function decorated with +up
type Up struct {
	Node         *ModTreeNode  `json:"node"`
	PortMappings []PortForward `json:"portMappings,omitempty"`
}

type UpGroup struct {
	Node *ModTreeNode `json:"node"`
	Ups  []*Up        `json:"ups"`

	// BoundWorkspace is the Workspace this group was rolled up from — the one
	// `Workspace.services` was called on, including any overlay edits. Run threads
	// it into the context (WorkspaceToContext) so each service leaf's auto-injected
	// Workspace! (and any currentWorkspace read) resolves against it, rather than
	// the session's frozen current workspace. Transient (not persisted): it is
	// re-established when `services` re-runs on replay.
	BoundWorkspace dagql.ObjectResult[*Workspace] `json:"-"`
}

func NewUpGroup(ctx context.Context, mod dagql.ObjectResult[*Module], include []string) (*UpGroup, error) {
	rootNode, err := NewModTree(ctx, mod)
	if err != nil {
		return nil, err
	}

	upNodes, err := rootNode.RollupUp(ctx, include, nil)
	if err != nil {
		return nil, err
	}
	ups := make([]*Up, 0, len(upNodes))
	for _, upNode := range upNodes {
		ups = append(ups, &Up{Node: upNode})
	}
	return &UpGroup{
		Node: rootNode,
		Ups:  ups,
	}, nil
}

func (*UpGroup) Type() *ast.Type {
	return &ast.Type{
		NamedType: "UpGroup",
		NonNull:   true,
	}
}

func (ug *UpGroup) List() []*Up {
	return ug.Ups
}

// Run starts all service functions in the group.
//
// Uses a two-phase approach: phase 1 starts all services in parallel and
// returns immediately once each is healthy; phase 2 blocks on ctx.Done().
// This ensures that if one service fails to start, the error is surfaced
// immediately without leaving sibling goroutines hanging forever.
//
// Each service evaluates its module function beneath its own display span
// (see ModTreeNode.RunUp), then parks at the group's start gate until every
// service has evaluated and the group's host ports are collision-free.
// Evaluating inside the display span matters beyond fail-fast ordering: the
// evaluation's API spans are what the service's log stream is routed to
// (dagui routes a service's stdio to the span that created its value), so
// this is what makes `dagger up` show each service's logs under its own row
// rather than under a separate preflight subtree.
func (ug *UpGroup) Run(ctx context.Context) (*UpGroup, error) {
	ug = ug.Clone()

	// Run the services against the workspace this group was rolled up from, so
	// overlay edits applied since the session loaded are visible to each service
	// (its auto-injected Workspace! and any currentWorkspace read resolve against
	// BoundWorkspace, not the frozen session workspace).
	if ug.BoundWorkspace.Self() != nil {
		ctx = WorkspaceToContext(ctx, ug.BoundWorkspace)
	}

	// Phase 1: start all services in parallel. Each RunUp evaluates the
	// module function, reports its ports to the gate, creates the host
	// tunnel, and waits for the health check — then returns immediately (no
	// blocking). The jobs themselves are untraced: each service's RunUp span
	// is its display span, and a wrapper job span would only duplicate it.
	gate := newUpStartGate(len(ug.Ups))
	var (
		mu      sync.Mutex
		results []*runUpStartResult
	)
	jobs := parallel.New().WithTracing(false)
	for _, up := range ug.Ups {
		jobs = jobs.WithJob(up.Name(), func(ctx context.Context) error {
			result, err := up.Node.RunUp(ctx, nil, nil, up.PortMappings, gate)
			if err != nil {
				return err
			}
			mu.Lock()
			results = append(results, result)
			mu.Unlock()
			return nil
		})
	}
	if err := jobs.Run(ctx); err != nil {
		// Clean up any ready spans from services that did start.
		for _, r := range results {
			r.ReadySpan.End()
		}
		return nil, err
	}

	// Phase 2: all services started successfully. Block until context
	// cancellation (e.g. Ctrl+C).
	<-ctx.Done()
	for _, r := range results {
		r.ReadySpan.End()
	}
	return ug, nil
}

// upStartGate is the barrier between evaluating an up group's services and
// starting them. Each service reports the host ports it wants (or aborts,
// if it failed before reaching the gate) and then blocks on the group-wide
// verdict: nothing starts until every service has reported and no two
// services claim the same host port. This replaces the old preflight
// pre-pass, which evaluated every service in a separate subtree — a subtree
// that then owned the services' routed log streams and appeared to run for
// the whole session.
type upStartGate struct {
	expected int

	mu      sync.Mutex
	reports []upPortReport
	aborted int

	done    chan struct{}
	verdict error
}

type upPortReport struct {
	name  string
	ports []upHostPort
}

type upHostPort struct {
	port     int
	protocol NetworkProtocol
}

func newUpStartGate(expected int) *upStartGate {
	return &upStartGate{
		expected: expected,
		done:     make(chan struct{}),
	}
}

// arrive reports name's host ports and blocks until every sibling has
// reported, then returns the group verdict. A ctx cancellation (e.g. a
// sibling failing under a fail-fast runner) aborts the wait.
func (g *upStartGate) arrive(ctx context.Context, name string, ports []upHostPort) error {
	g.mu.Lock()
	g.reports = append(g.reports, upPortReport{name: name, ports: ports})
	g.noteReportLocked()
	g.mu.Unlock()

	select {
	case <-g.done:
		return g.verdict
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// abort reports that a service failed before reaching the gate (e.g. its
// evaluation failed), releasing waiting siblings with an error verdict so
// the group never partially starts when it's known to be doomed.
func (g *upStartGate) abort() {
	g.mu.Lock()
	g.aborted++
	g.noteReportLocked()
	g.mu.Unlock()
}

func (g *upStartGate) noteReportLocked() {
	if len(g.reports)+g.aborted != g.expected {
		return
	}
	g.verdict = g.verdictLocked()
	close(g.done)
}

func (g *upStartGate) verdictLocked() error {
	if g.aborted > 0 {
		return errors.New("not started: another service in the group failed")
	}

	// Deterministic output regardless of arrival order.
	reports := slices.Clone(g.reports)
	sort.Slice(reports, func(i, j int) bool { return reports[i].name < reports[j].name })

	seen := make(map[upHostPort]string) // port → first service name
	var conflicts []string
	for _, report := range reports {
		for _, port := range report.ports {
			if first, ok := seen[port]; ok {
				conflicts = append(conflicts, fmt.Sprintf(
					"port %d/%s is exposed by both %q and %q",
					port.port, strings.ToLower(string(port.protocol)), first, report.name,
				))
			} else {
				seen[port] = report.name
			}
		}
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("port collision detected:\n  %s", strings.Join(conflicts, "\n  "))
	}
	return nil
}

func (ug *UpGroup) Clone() *UpGroup {
	cp := *ug
	if cp.Node != nil {
		cp.Node = cp.Node.Clone()
	}
	cp.Ups = make([]*Up, len(ug.Ups))
	for i := range cp.Ups {
		cp.Ups[i] = ug.Ups[i].Clone()
	}
	return &cp
}

func (u *Up) Path() []string {
	return u.Node.Path()
}

func (u *Up) Description() string {
	return u.Node.Description
}

func (u *Up) OriginalModule() *Module {
	return u.Node.OriginalModule.Self()
}

func (*Up) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Up",
		NonNull:   true,
	}
}

func (u *Up) Name() string {
	return u.Node.PathString()
}

func (u *Up) Clone() *Up {
	cp := *u
	cp.Node = u.Node.Clone()
	return &cp
}

// Run starts the service returned by this up function and blocks until ctx is cancelled.
func (u *Up) Run(ctx context.Context) (*Up, error) {
	u = u.Clone()
	result, err := u.Node.RunUp(ctx, nil, nil, u.PortMappings, nil)
	if err != nil {
		return u, err
	}
	defer result.ReadySpan.End()
	<-ctx.Done()
	return u, nil
}
