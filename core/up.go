package core

import (
	"context"
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
}

func NewUpGroup(ctx context.Context, mod *Module, include []string) (*UpGroup, error) {
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
// Before starting, it evaluates all services to detect port collisions.
//
// Uses a two-phase approach: phase 1 starts all services in parallel and
// returns immediately once each is healthy; phase 2 blocks on ctx.Done().
// This ensures that if one service fails to start, the error is surfaced
// immediately without leaving sibling goroutines hanging forever.
func (ug *UpGroup) Run(ctx context.Context) (*UpGroup, error) {
	ug = ug.Clone()

	if err := ug.checkPortCollisions(ctx); err != nil {
		return nil, err
	}

	// Phase 1: start all services in parallel. Each RunUp evaluates the
	// module function, creates the host tunnel, and waits for the health
	// check — then returns immediately (no blocking).
	var (
		mu      sync.Mutex
		results []*runUpStartResult
	)
	jobs := parallel.New().WithContextualTracer(true)
	for _, up := range ug.Ups {
		jobs = jobs.WithJob(up.Name(), func(ctx context.Context) error {
			result, err := up.Node.RunUp(ctx, nil, nil, up.PortMappings)
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

// checkPortCollisions evaluates all service functions to collect their exposed
// ports and fails fast if two services expose the same host port.
func (ug *UpGroup) checkPortCollisions(ctx context.Context) error {
	type portKey struct {
		port     int
		protocol NetworkProtocol
	}

	// Evaluate all services in parallel to collect ports.
	// NOTE: the same DagqlValue() call happens again in runUpLocally during
	// Run(). This is safe because dagql caches Select results by content
	// address, so the second evaluation is a cache hit with no re-execution.
	type servicePort struct {
		name string
		port portKey
	}
	var (
		mu       = new(sync.Mutex)
		allPorts []servicePort
	)

	jobs := parallel.New().WithContextualTracer(true)
	for _, up := range ug.Ups {
		jobs = jobs.WithJob(up.Name()+":preflight", func(ctx context.Context) error {
			// If port mappings are configured, use the frontend (host) ports
			// for collision detection instead of the container ports.
			if len(up.PortMappings) > 0 {
				mu.Lock()
				defer mu.Unlock()
				for _, pf := range up.PortMappings {
					hostPort := pf.Backend
					if pf.Frontend != nil {
						hostPort = *pf.Frontend
					}
					allPorts = append(allPorts, servicePort{
						name: up.Name(),
						port: portKey{port: hostPort, protocol: pf.Protocol},
					})
				}
				return nil
			}

			var svcResult dagql.ObjectResult[*Service]
			if err := up.Node.DagqlValue(ctx, &svcResult); err != nil {
				return err
			}
			svc := svcResult.Self()
			if svc == nil || svc.Container == nil {
				return nil
			}
			mu.Lock()
			defer mu.Unlock()
			for _, p := range svc.Container.Ports {
				allPorts = append(allPorts, servicePort{
					name: up.Name(),
					port: portKey{port: p.Port, protocol: p.Protocol},
				})
			}
			return nil
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return err
	}

	// Check for duplicates.
	seen := make(map[portKey]string) // port → first service name
	var conflicts []string
	for _, sp := range allPorts {
		if first, ok := seen[sp.port]; ok {
			conflicts = append(conflicts, fmt.Sprintf(
				"port %d/%s is exposed by both %q and %q",
				sp.port.port, strings.ToLower(string(sp.port.protocol)), first, sp.name,
			))
		} else {
			seen[sp.port] = sp.name
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
	return u.Node.OriginalModule
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
	result, err := u.Node.RunUp(ctx, nil, nil, u.PortMappings)
	if err != nil {
		return u, err
	}
	defer result.ReadySpan.End()
	<-ctx.Done()
	return u, nil
}

// AsService evaluates the underlying +up-tagged module function and returns
// the Service it produces. The returned ObjectResult carries the dagql ID of
// the original user function, so downstream calls (hostname, start, up, ...)
// share cache identity with direct invocations of that function.
func (u *Up) AsService(ctx context.Context) (dagql.ObjectResult[*Service], error) {
	var svcResult dagql.ObjectResult[*Service]
	if err := u.Node.DagqlValue(ctx, &svcResult); err != nil {
		return svcResult, fmt.Errorf("%q: evaluate service: %w", u.Name(), err)
	}
	return svcResult, nil
}

// AsServices evaluates each Up in the group and returns the Services they
// produce, in the same order as List(). Fails fast on the first evaluation
// error.
func (ug *UpGroup) AsServices(ctx context.Context) ([]dagql.ObjectResult[*Service], error) {
	services := make([]dagql.ObjectResult[*Service], 0, len(ug.Ups))
	for _, up := range ug.Ups {
		svc, err := up.AsService(ctx)
		if err != nil {
			return nil, err
		}
		services = append(services, svc)
	}
	return services, nil
}
