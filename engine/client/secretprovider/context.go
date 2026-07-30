package secretprovider

import "context"

type workspaceRootKey struct{}

// withWorkspaceRoot attaches the client's workspace root (the directory
// containing dagger.json, if any) to ctx, for providers that need to resolve
// paths consistently regardless of the client's current working directory.
func withWorkspaceRoot(ctx context.Context, root string) context.Context {
	if root == "" {
		return ctx
	}
	return context.WithValue(ctx, workspaceRootKey{}, root)
}

// workspaceRootFromContext returns the workspace root attached by
// withWorkspaceRoot, or "" if none was set.
func workspaceRootFromContext(ctx context.Context) string {
	root, _ := ctx.Value(workspaceRootKey{}).(string)
	return root
}
