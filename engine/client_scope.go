package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// ClientLeaseKind identifies why executable client state is retained. These
// lifecycle leases are intentionally unrelated to BuildKit or DagQL operation
// leases, which protect snapshots and other operation resources.
type ClientLeaseKind string

const (
	ClientLeaseTransport  ClientLeaseKind = "transport"
	ClientLeaseRequest    ClientLeaseKind = "request"
	ClientLeaseAgent      ClientLeaseKind = "agent"
	ClientLeaseService    ClientLeaseKind = "service"
	ClientLeaseSharedWork ClientLeaseKind = "shared-work"
	ClientLeaseChild      ClientLeaseKind = "child"
)

// ClientScopeAuthority is an opaque session identity used for strict
// capability delegation.
type ClientScopeAuthority struct{ identity byte }

// NewClientScopeAuthority creates an unforgeable-by-identity session token used
// to reject scopes retained from an older session record with reused string IDs.
func NewClientScopeAuthority() *ClientScopeAuthority { return &ClientScopeAuthority{} }

// ClientLifecycleLease is an idempotent handle retaining one client runtime for
// a specific owner and reason.
type ClientLifecycleLease struct {
	kind      ClientLeaseKind
	ownerID   string
	held      atomic.Bool
	once      sync.Once
	release   func()
	clone     func(ClientLeaseKind, string) (*ClientLifecycleLease, error)
	delegate  func(string, string, ClientLeaseKind, string) (*ClientLifecycleLease, error)
	authority *ClientScopeAuthority
}

// NewClientLifecycleLease constructs a lifecycle lease. The release and clone
// callbacks are supplied by the owning session, which serializes both against
// client closing and session teardown.
func NewClientLifecycleLease(
	kind ClientLeaseKind,
	ownerID string,
	release func(),
	clone func(ClientLeaseKind, string) (*ClientLifecycleLease, error),
) *ClientLifecycleLease {
	return newClientLifecycleLease(kind, ownerID, release, clone, nil)
}

// NewClientLifecycleLeaseWithDelegation constructs a lifecycle lease bound to
// one session-owned client record. delegate must atomically validate the source
// identity and ownership before cloning it; unlike Clone, Delegate never accepts
// an unbound or synthetic lease.
func NewClientLifecycleLeaseWithDelegation(
	kind ClientLeaseKind,
	ownerID string,
	release func(),
	clone func(ClientLeaseKind, string) (*ClientLifecycleLease, error),
	delegate func(string, string, ClientLeaseKind, string) (*ClientLifecycleLease, error),
	authority *ClientScopeAuthority,
) *ClientLifecycleLease {
	lease := newClientLifecycleLease(kind, ownerID, release, clone, delegate)
	lease.authority = authority
	return lease
}

func newClientLifecycleLease(
	kind ClientLeaseKind,
	ownerID string,
	release func(),
	clone func(ClientLeaseKind, string) (*ClientLifecycleLease, error),
	delegate func(string, string, ClientLeaseKind, string) (*ClientLifecycleLease, error),
) *ClientLifecycleLease {
	lease := &ClientLifecycleLease{
		kind:     kind,
		ownerID:  ownerID,
		release:  release,
		clone:    clone,
		delegate: delegate,
	}
	lease.held.Store(true)
	return lease
}

func (lease *ClientLifecycleLease) Kind() ClientLeaseKind {
	if lease == nil {
		return ""
	}
	return lease.kind
}

func (lease *ClientLifecycleLease) OwnerID() string {
	if lease == nil {
		return ""
	}
	return lease.ownerID
}

func (lease *ClientLifecycleLease) Held() bool {
	return lease != nil && lease.held.Load()
}

// Release relinquishes the lease exactly once and is safe to call
// concurrently.
func (lease *ClientLifecycleLease) Release() {
	if lease == nil {
		return
	}
	lease.once.Do(func() {
		lease.held.Store(false)
		if lease.release != nil {
			lease.release()
		}
	})
}

// ClientScope is an immutable client identity and metadata snapshot paired
// with one held lifecycle lease. Copies are safe: they refer to the same
// idempotent lease, while Clone acquires independent ownership.
type ClientScope struct {
	sessionID             string
	clientID              string
	metadata              []byte
	useRecipeIDsByDefault bool
	lease                 *ClientLifecycleLease
}

// NewClientScope snapshots metadata so later bootstrap mutations cannot change
// work that has already detached from the request that launched it.
func NewClientScope(metadata *ClientMetadata, lease *ClientLifecycleLease) (ClientScope, error) {
	if metadata == nil {
		return ClientScope{}, errors.New("client scope metadata is nil")
	}
	if metadata.SessionID == "" || metadata.ClientID == "" {
		return ClientScope{}, errors.New("client scope metadata is missing session or client ID")
	}
	if lease == nil || !lease.Held() {
		return ClientScope{}, errors.New("client scope requires a held lifecycle lease")
	}
	snapshot, err := json.Marshal(metadata)
	if err != nil {
		return ClientScope{}, fmt.Errorf("snapshot client scope metadata: %w", err)
	}
	return ClientScope{
		sessionID:             metadata.SessionID,
		clientID:              metadata.ClientID,
		metadata:              snapshot,
		useRecipeIDsByDefault: metadata.UseRecipeIDsByDefault,
		lease:                 lease,
	}, nil
}

func (scope ClientScope) SessionID() string { return scope.sessionID }
func (scope ClientScope) ClientID() string  { return scope.clientID }

func (scope ClientScope) Lease() *ClientLifecycleLease { return scope.lease }

// Metadata returns an independent copy of the scope's sealed metadata.
func (scope ClientScope) Metadata() (*ClientMetadata, error) {
	if len(scope.metadata) == 0 {
		return nil, errors.New("client scope has no metadata snapshot")
	}
	var metadata ClientMetadata
	if err := json.Unmarshal(scope.metadata, &metadata); err != nil {
		return nil, fmt.Errorf("restore client scope metadata: %w", err)
	}
	metadata.UseRecipeIDsByDefault = scope.useRecipeIDsByDefault
	return &metadata, nil
}

// Clone acquires an independent lease from an already-held scope. The owning
// session validates the source lease under its lifecycle serialization point,
// so cloning remains valid while the client is closing but loses the race to a
// source release or session teardown.
func (scope ClientScope) Clone(kind ClientLeaseKind, ownerID string) (ClientScope, error) {
	if scope.lease == nil || !scope.lease.Held() || scope.lease.clone == nil {
		return ClientScope{}, errors.New("client scope lease is not held")
	}
	lease, err := scope.lease.clone(kind, ownerID)
	if err != nil {
		return ClientScope{}, err
	}
	return ClientScope{
		sessionID:             scope.sessionID,
		clientID:              scope.clientID,
		metadata:              append([]byte(nil), scope.metadata...),
		useRecipeIDsByDefault: scope.useRecipeIDsByDefault,
		lease:                 lease,
	}, nil
}

// CanDelegateTo reports whether this scope belongs to the exact live session
// authority, not merely a record with equal session and client ID strings.
func (scope ClientScope) CanDelegateTo(authority *ClientScopeAuthority) bool {
	return authority != nil && scope.lease != nil && scope.lease.Held() && scope.lease.authority == authority
}

// Delegate clones a scope through its owning session's strict capability
// boundary. It rejects synthetic, stale, released, or identity-mismatched
// scopes instead of treating their IDs as authority.
func (scope ClientScope) Delegate(kind ClientLeaseKind, ownerID string) (ClientScope, error) {
	if scope.lease == nil || !scope.lease.Held() || scope.lease.delegate == nil {
		return ClientScope{}, errors.New("client scope is not a held delegation capability")
	}
	lease, err := scope.lease.delegate(scope.sessionID, scope.clientID, kind, ownerID)
	if err != nil {
		return ClientScope{}, err
	}
	return ClientScope{
		sessionID:             scope.sessionID,
		clientID:              scope.clientID,
		metadata:              append([]byte(nil), scope.metadata...),
		useRecipeIDsByDefault: scope.useRecipeIDsByDefault,
		lease:                 lease,
	}, nil
}

type NestedClientTransport struct {
	closed atomic.Bool
	once   sync.Once
	close  func()
}

// NewNestedClientTransport constructs the opaque ownership handle returned by
// nested transport registration. Callers can only close or inspect it; the
// registering server keeps the associated client identity private.
func NewNestedClientTransport(close func()) *NestedClientTransport {
	return &NestedClientTransport{close: close}
}

// Close marks nested reachability closed and relinquishes transport ownership
// exactly once. It is safe to call concurrently and is also used by /shutdown.
func (transport *NestedClientTransport) Close() {
	if transport == nil {
		return
	}
	transport.once.Do(func() {
		transport.closed.Store(true)
		if transport.close != nil {
			transport.close()
		}
	})
}

func (transport *NestedClientTransport) Closed() bool {
	return transport == nil || transport.closed.Load()
}

type clientScopeContextKey struct{}

// ContextWithClientScope installs the immutable scope and its metadata
// snapshot. Existing metadata values are replaced by the sealed snapshot.
func ContextWithClientScope(ctx context.Context, scope ClientScope) (context.Context, error) {
	metadata, err := scope.Metadata()
	if err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, clientScopeContextKey{}, scope)
	return ContextWithClientMetadata(ctx, metadata), nil
}

func ClientScopeFromContext(ctx context.Context) (ClientScope, bool) {
	scope, ok := ctx.Value(clientScopeContextKey{}).(ClientScope)
	return scope, ok
}

// DetachClientScope explicitly clones lifecycle ownership before removing the
// caller's cancellation. Contexts without a scope are supported for standalone
// DagQL/core use and return no lease; engine request contexts always carry one.
func DetachClientScope(ctx context.Context, kind ClientLeaseKind, ownerID string) (context.Context, *ClientLifecycleLease, error) {
	scope, ok := ClientScopeFromContext(ctx)
	if !ok {
		return context.WithoutCancel(ctx), nil, nil
	}
	cloned, err := scope.Clone(kind, ownerID)
	if err != nil {
		return nil, nil, err
	}
	detached, err := ContextWithClientScope(context.WithoutCancel(ctx), cloned)
	if err != nil {
		cloned.Lease().Release()
		return nil, nil, err
	}
	return detached, cloned.Lease(), nil
}
