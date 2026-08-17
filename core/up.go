package core

import (
	"context"
	"encoding/json"
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

type DetachedUpResult struct {
	Services []DetachedUpService `json:"services"`
}

type DetachedUpService struct {
	Name           string           `json:"name"`
	ServiceID      string           `json:"serviceId"`
	Native         bool             `json:"native"`
	PortMappings   []PortForward    `json:"portMappings"`
	BackendPorts   []DetachedUpPort `json:"backendPorts"`
	effectivePorts []PortForward
}

type DetachedUpPort struct {
	Port     int             `json:"port"`
	Protocol NetworkProtocol `json:"protocol"`
}

type evaluatedDetachedUp struct {
	service dagql.ObjectResult[*Service]
	payload DetachedUpService
}

type upPortKey struct {
	port     int
	protocol NetworkProtocol
}

type upServicePort struct {
	name string
	port upPortKey
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
// Before starting, it evaluates all services to detect port collisions.
//
// Uses a two-phase approach: phase 1 starts all services in parallel and
// returns immediately once each is healthy; phase 2 blocks on ctx.Done().
// This ensures that if one service fails to start, the error is surfaced
// immediately without leaving sibling goroutines hanging forever.
func (ug *UpGroup) Run(ctx context.Context) (*UpGroup, error) {
	ug = ug.Clone()

	// Run the services against the workspace this group was rolled up from, so
	// overlay edits applied since the session loaded are visible to each service
	// (its auto-injected Workspace! and any currentWorkspace read resolve against
	// BoundWorkspace, not the frozen session workspace).
	if ug.BoundWorkspace.Self() != nil {
		ctx = WorkspaceToContext(ctx, ug.BoundWorkspace)
	}

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

// StartDetached starts and retains the backend half of every selected +up
// service without creating host tunnels. The returned JSON is the durable
// recipe later attachments use to recreate local port publication.
func (ug *UpGroup) StartDetached(ctx context.Context) (JSON, error) {
	ug = ug.Clone()
	if ug.BoundWorkspace.Self() != nil {
		ctx = WorkspaceToContext(ctx, ug.BoundWorkspace)
	}

	// Evaluate and validate the complete set before installing any retention
	// mark or starting any backend.
	evaluated := make([]evaluatedDetachedUp, len(ug.Ups))
	jobs := parallel.New().WithContextualTracer(true)
	for i, up := range ug.Ups {
		jobs = jobs.WithJob(up.Name()+":detached-preflight", func(ctx context.Context) error {
			var svcResult dagql.ObjectResult[*Service]
			if err := up.Node.DagqlValue(ctx, &svcResult); err != nil {
				return err
			}
			payload, err := prepareDetachedUpService(up.Name(), up.PortMappings, svcResult)
			if err != nil {
				return err
			}
			evaluated[i] = evaluatedDetachedUp{service: svcResult, payload: payload}
			return nil
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return nil, err
	}

	allPorts := make([]upServicePort, 0)
	for _, evaluatedUp := range evaluated {
		for _, mapping := range evaluatedUp.payload.effectivePorts {
			allPorts = append(allPorts, upServicePort{
				name: evaluatedUp.payload.Name,
				port: upPortKey{port: mapping.FrontendOrBackendPort(), protocol: mapping.Protocol},
			})
		}
	}
	if err := checkUpPortCollisions(allPorts); err != nil {
		return nil, err
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	services, err := query.Services(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get services: %w", err)
	}
	jobs = parallel.New().WithContextualTracer(true)
	for _, evaluatedUp := range evaluated {
		jobs = jobs.WithJob(evaluatedUp.payload.Name, func(ctx context.Context) error {
			_, release, err := services.StartRetainedResult(ctx, evaluatedUp.service, evaluatedUp.payload.Name)
			if err != nil {
				return err
			}
			release()
			return nil
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return nil, err
	}

	result := DetachedUpResult{Services: make([]DetachedUpService, len(evaluated))}
	for i := range evaluated {
		result.Services[i] = evaluated[i].payload
		result.Services[i].effectivePorts = nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode detached up services: %w", err)
	}
	return JSON(encoded), nil
}

func prepareDetachedUpService(
	name string,
	configured []PortForward,
	service dagql.ObjectResult[*Service],
) (DetachedUpService, error) {
	svc := service.Self()
	if svc == nil || svc.Container.Self() == nil {
		return DetachedUpService{}, fmt.Errorf("service %q: tunneling to non-Container services is not supported", name)
	}

	serviceID, err := service.ID()
	if err != nil {
		return DetachedUpService{}, fmt.Errorf("service %q ID: %w", name, err)
	}
	encodedID, err := serviceID.Encode()
	if err != nil {
		return DetachedUpService{}, fmt.Errorf("encode service %q ID: %w", name, err)
	}

	payload := DetachedUpService{
		Name:         name,
		ServiceID:    encodedID,
		Native:       len(configured) == 0,
		PortMappings: []PortForward{},
		BackendPorts: make([]DetachedUpPort, 0, len(svc.Container.Self().Ports)),
	}
	for _, port := range svc.Container.Self().Ports {
		protocol := port.Protocol
		if protocol == "" {
			protocol = NetworkProtocolTCP
		}
		payload.BackendPorts = append(payload.BackendPorts, DetachedUpPort{
			Port: port.Port, Protocol: protocol,
		})
	}

	if len(configured) == 0 {
		payload.effectivePorts = make([]PortForward, 0, len(payload.BackendPorts))
		for _, port := range payload.BackendPorts {
			frontend := port.Port
			payload.effectivePorts = append(payload.effectivePorts, PortForward{
				Frontend: &frontend, Backend: port.Port, Protocol: port.Protocol,
			})
		}
	} else {
		payload.PortMappings = make([]PortForward, len(configured))
		payload.effectivePorts = make([]PortForward, len(configured))
		for i, mapping := range configured {
			if mapping.Frontend == nil {
				return DetachedUpService{}, fmt.Errorf("service %q port mapping for backend %d must have a fixed frontend", name, mapping.Backend)
			}
			frontend := *mapping.Frontend
			protocol := mapping.Protocol
			if protocol == "" {
				protocol = NetworkProtocolTCP
			}
			mapping = PortForward{Frontend: &frontend, Backend: mapping.Backend, Protocol: protocol}
			payload.PortMappings[i] = mapping
			payload.effectivePorts[i] = mapping
		}
	}

	if len(payload.effectivePorts) == 0 {
		return DetachedUpService{}, fmt.Errorf("service %q: no ports to forward", name)
	}
	for _, mapping := range payload.effectivePorts {
		if mapping.Protocol == NetworkProtocolUDP {
			return DetachedUpService{}, fmt.Errorf("service %q: UDP port forwarding is not supported", name)
		}
	}
	return payload, nil
}

// checkPortCollisions evaluates all service functions to collect their exposed
// ports and fails fast if two services expose the same host port.
func (ug *UpGroup) checkPortCollisions(ctx context.Context) error {
	// Evaluate all services in parallel to collect ports.
	// NOTE: the same DagqlValue() call happens again in runUpLocally during
	// Run(). This is safe because dagql caches Select results by content
	// address, so the second evaluation is a cache hit with no re-execution.
	var (
		mu       = new(sync.Mutex)
		allPorts []upServicePort
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
					allPorts = append(allPorts, upServicePort{
						name: up.Name(),
						port: upPortKey{port: hostPort, protocol: pf.Protocol},
					})
				}
				return nil
			}

			var svcResult dagql.ObjectResult[*Service]
			if err := up.Node.DagqlValue(ctx, &svcResult); err != nil {
				return err
			}
			svc := svcResult.Self()
			if svc == nil || svc.Container.Self() == nil {
				return nil
			}
			mu.Lock()
			defer mu.Unlock()
			for _, p := range svc.Container.Self().Ports {
				allPorts = append(allPorts, upServicePort{
					name: up.Name(),
					port: upPortKey{port: p.Port, protocol: p.Protocol},
				})
			}
			return nil
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return err
	}

	return checkUpPortCollisions(allPorts)
}

func checkUpPortCollisions(allPorts []upServicePort) error {
	seen := make(map[upPortKey]string) // port → first service name
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
	result, err := u.Node.RunUp(ctx, nil, nil, u.PortMappings)
	if err != nil {
		return u, err
	}
	defer result.ReadySpan.End()
	<-ctx.Done()
	return u, nil
}
