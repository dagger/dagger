package core

import (
	"context"
	"errors"
	"fmt"
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

// Run starts all service functions in the group and blocks until ctx is
// cancelled (e.g. Ctrl+C).
//
// It runs in two phases. Phase 1 evaluates every service in parallel, each
// beneath its own display span (see ModTreeNode.PrepareUp) — evaluating
// there matters beyond ordering: the evaluation's API spans are what the
// service's log stream is routed to (dagui routes a service's stdio to the
// span that created its value), so this is what makes `dagger up` show each
// service's logs under its own row rather than under a separate preflight
// subtree. Nothing starts until every service has evaluated and the group's
// host ports are collision-free. Phase 2 then starts them all in parallel,
// returning from each as soon as it is healthy, so a service that fails to
// start surfaces immediately without leaving sibling goroutines hanging.
func (ug *UpGroup) Run(ctx context.Context) (*UpGroup, error) {
	ug = ug.Clone()

	// Run the services against the workspace this group was rolled up from, so
	// overlay edits applied since the session loaded are visible to each service
	// (its auto-injected Workspace! and any currentWorkspace read resolve against
	// BoundWorkspace, not the frozen session workspace).
	if ug.BoundWorkspace.Self() != nil {
		ctx = WorkspaceToContext(ctx, ug.BoundWorkspace)
	}

	// Phase 1: evaluate every service beneath its own display span, in
	// parallel, collecting the host ports each wants. The jobs themselves are
	// untraced: each service's display span is its row, and a wrapper job
	// span would only duplicate it.
	preps := make([]*preparedUp, len(ug.Ups))
	jobs := parallel.New().WithTracing(false)
	for i, up := range ug.Ups {
		jobs = jobs.WithJob(up.Name(), func(ctx context.Context) error {
			prep, err := up.Node.PrepareUp(ctx, up.PortMappings)
			if err != nil {
				return err
			}
			preps[i] = prep
			return nil
		})
	}
	// abort ends every prepared service's display span without starting it —
	// the group never partially starts when it's known to be doomed.
	abort := func(cause error) {
		for _, prep := range preps {
			if prep != nil {
				prep.Abort(cause)
			}
		}
	}
	if err := jobs.Run(ctx); err != nil {
		abort(errors.New("not started: another service in the group failed"))
		return nil, err
	}

	// Verdict: refuse to start anything when two services claim the same
	// host port.
	if err := checkPortCollisions(preps); err != nil {
		abort(err)
		return nil, err
	}

	// Phase 2: start all services in parallel. Each Start creates the host
	// tunnel and waits for the health check — then returns immediately (no
	// blocking).
	var (
		mu      sync.Mutex
		results []*runUpStartResult
	)
	jobs = parallel.New().WithTracing(false)
	for _, prep := range preps {
		jobs = jobs.WithJob(prep.Name(), func(ctx context.Context) error {
			result, err := prep.Start(ctx)
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

	// All services started successfully. Block until context cancellation
	// (e.g. Ctrl+C).
	<-ctx.Done()
	for _, r := range results {
		r.ReadySpan.End()
	}
	return ug, nil
}

// checkPortCollisions reports an error when two prepared services claim the
// same host port. preps follows the group's declaration order, so the
// output is deterministic.
func checkPortCollisions(preps []*preparedUp) error {
	seen := make(map[upHostPort]string) // port → first service name
	var conflicts []string
	for _, prep := range preps {
		for _, port := range prep.hostPorts {
			if first, ok := seen[port]; ok {
				conflicts = append(conflicts, fmt.Sprintf(
					"port %d/%s is exposed by both %q and %q",
					port.port, strings.ToLower(string(port.protocol)), first, prep.Name(),
				))
			} else {
				seen[port] = prep.Name()
			}
		}
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("port collision detected:\n  %s", strings.Join(conflicts, "\n  "))
	}
	return nil
}

type upHostPort struct {
	port     int
	protocol NetworkProtocol
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
	prep, err := u.Node.PrepareUp(ctx, u.PortMappings)
	if err != nil {
		return u, err
	}
	result, err := prep.Start(ctx)
	if err != nil {
		return u, err
	}
	defer result.ReadySpan.End()
	<-ctx.Done()
	return u, nil
}
