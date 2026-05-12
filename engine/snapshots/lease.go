package snapshots

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/pkg/errors"
)

type lazyLeaseScopeKey struct{}
type withoutLazyLeaseScope struct{}

type lazyLeaseScope struct {
	mu       sync.Mutex
	lm       leases.Manager
	opts     []leases.Opt
	lease    *LeaseRef
	released bool
}

func WithLazyLease(ctx context.Context, lm leases.Manager, opts ...leases.Opt) (context.Context, func(context.Context) error, error) {
	if leaseID, ok := leases.FromContext(ctx); ok && leaseID != "" {
		return ctx, func(context.Context) error { return nil }, nil
	}
	if lm == nil {
		return ctx, func(context.Context) error { return nil }, nil
	}
	if lazyLeaseFromContext(ctx) != nil {
		return ctx, func(context.Context) error { return nil }, nil
	}

	scope := &lazyLeaseScope{
		lm:   lm,
		opts: append([]leases.Opt(nil), opts...),
	}
	return context.WithValue(ctx, lazyLeaseScopeKey{}, scope), scope.release, nil
}

func EnsureLease(ctx context.Context) (context.Context, error) {
	if leaseID, ok := leases.FromContext(ctx); ok && leaseID != "" {
		return ctx, nil
	}
	scope := lazyLeaseFromContext(ctx)
	if scope == nil {
		return ctx, nil
	}
	return scope.ensure(ctx)
}

func HasLazyLease(ctx context.Context) bool {
	return lazyLeaseFromContext(ctx) != nil
}

func WithoutLazyLease(ctx context.Context) context.Context {
	if lazyLeaseFromContext(ctx) == nil {
		return ctx
	}
	return context.WithValue(ctx, lazyLeaseScopeKey{}, withoutLazyLeaseScope{})
}

func lazyLeaseFromContext(ctx context.Context) *lazyLeaseScope {
	scope, _ := ctx.Value(lazyLeaseScopeKey{}).(*lazyLeaseScope)
	return scope
}

func (s *lazyLeaseScope) ensure(ctx context.Context) (context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.released {
		return ctx, fmt.Errorf("operation lease scope already released")
	}
	if s.lease != nil {
		return leases.WithLease(ctx, s.lease.l.ID), nil
	}
	lease, leaseCtx, err := NewLease(ctx, s.lm, s.opts...)
	if err != nil {
		return ctx, err
	}
	s.lease = lease
	return leaseCtx, nil
}

func (s *lazyLeaseScope) release(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.released {
		return nil
	}
	s.released = true
	if s.lease == nil {
		return nil
	}
	return s.lm.Delete(ctx, s.lease.l)
}

func WithLease(ctx context.Context, ls leases.Manager, opts ...leases.Opt) (context.Context, func(context.Context) error, error) {
	leaseID, ok := leases.FromContext(ctx)
	if ok && leaseID != "" {
		return ctx, func(context.Context) error {
			return nil
		}, nil
	}

	lr, ctx, err := NewLease(ctx, ls, opts...)
	if err != nil {
		return ctx, nil, err
	}

	return ctx, func(ctx context.Context) error {
		return ls.Delete(ctx, lr.l)
	}, nil
}

func NewLease(ctx context.Context, lm leases.Manager, opts ...leases.Opt) (*LeaseRef, context.Context, error) {
	l, err := lm.Create(ctx, append([]leases.Opt{leases.WithRandomID(), leases.WithExpiration(time.Hour)}, opts...)...)
	if err != nil {
		return nil, ctx, err
	}

	ctx = leases.WithLease(ctx, l.ID)
	return &LeaseRef{lm: lm, l: l}, ctx, nil
}

type LeaseRef struct {
	lm leases.Manager
	l  leases.Lease

	once      sync.Once
	resources []leases.Resource
	err       error
}

func (l *LeaseRef) Discard() error {
	return l.lm.Delete(context.Background(), l.l)
}

func (l *LeaseRef) Adopt(ctx context.Context) error {
	l.once.Do(func() {
		resources, err := l.lm.ListResources(ctx, l.l)
		if err != nil {
			l.err = err
			return
		}
		l.resources = resources
	})
	if l.err != nil {
		return l.err
	}
	currentID, ok := leases.FromContext(ctx)
	if !ok || currentID == "" {
		return errors.Errorf("missing lease requirement for adopt")
	}
	for _, r := range l.resources {
		if err := l.lm.AddResource(ctx, leases.Lease{ID: currentID}, r); err != nil {
			return err
		}
	}
	if len(l.resources) == 0 {
		l.Discard()
		return nil
	}
	go l.Discard()
	return nil
}

func MakeTemporary(l *leases.Lease) error {
	if l.Labels == nil {
		l.Labels = map[string]string{}
	}
	l.Labels["buildkit/lease.temporary"] = time.Now().UTC().Format(time.RFC3339Nano)
	return nil
}

func NewLeaseManager(lm leases.Manager, ns string) *LeaseManager {
	return &LeaseManager{manager: lm, ns: ns}
}

type LeaseManager struct {
	manager leases.Manager
	ns      string
}

func (l *LeaseManager) Namespace() string {
	return l.ns
}

func (l *LeaseManager) WithNamespace(ns string) *LeaseManager {
	return NewLeaseManager(l.manager, ns)
}

func (l *LeaseManager) Create(ctx context.Context, opts ...leases.Opt) (leases.Lease, error) {
	ctx = namespaces.WithNamespace(ctx, l.ns)
	return l.manager.Create(ctx, opts...)
}

func (l *LeaseManager) Delete(ctx context.Context, lease leases.Lease, opts ...leases.DeleteOpt) error {
	ctx = namespaces.WithNamespace(ctx, l.ns)
	return l.manager.Delete(ctx, lease, opts...)
}

func (l *LeaseManager) List(ctx context.Context, filters ...string) ([]leases.Lease, error) {
	ctx = namespaces.WithNamespace(ctx, l.ns)
	return l.manager.List(ctx, filters...)
}

func (l *LeaseManager) AddResource(ctx context.Context, lease leases.Lease, resource leases.Resource) error {
	ctx = namespaces.WithNamespace(ctx, l.ns)
	return l.manager.AddResource(ctx, lease, resource)
}

func (l *LeaseManager) DeleteResource(ctx context.Context, lease leases.Lease, resource leases.Resource) error {
	ctx = namespaces.WithNamespace(ctx, l.ns)
	return l.manager.DeleteResource(ctx, lease, resource)
}

func (l *LeaseManager) ListResources(ctx context.Context, lease leases.Lease) ([]leases.Resource, error) {
	ctx = namespaces.WithNamespace(ctx, l.ns)
	return l.manager.ListResources(ctx, lease)
}
