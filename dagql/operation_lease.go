package dagql

import (
	"context"

	"github.com/containerd/containerd/v2/core/leases"
)

type OperationLeaseProvider interface {
	WithOperationLease(context.Context) (context.Context, func(context.Context) error, error)
}

type OperationLeaseProviderFunc func(context.Context) (context.Context, func(context.Context) error, error)

func (f OperationLeaseProviderFunc) WithOperationLease(ctx context.Context) (context.Context, func(context.Context) error, error) {
	return f(ctx)
}

type operationLeaseProviderKey struct{}

func ContextWithOperationLeaseProvider(ctx context.Context, provider OperationLeaseProvider) context.Context {
	return context.WithValue(ctx, operationLeaseProviderKey{}, provider)
}

func withOperationLease(ctx context.Context) (context.Context, func(context.Context) error, error) {
	if _, ok := leases.FromContext(ctx); ok {
		return ctx, func(context.Context) error { return nil }, nil
	}
	provider, _ := ctx.Value(operationLeaseProviderKey{}).(OperationLeaseProvider)
	if provider == nil {
		return ctx, func(context.Context) error { return nil }, nil
	}
	return provider.WithOperationLease(ctx)
}
