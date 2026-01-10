package schema

// Git Lazy Resolution
//
// Git operations are lazy: git("github.com/foo").branch("main") doesn't hit the network.
// Resolution happens when you call tree(), commit(), etc. At that point, we may discover
// the URL needs a protocol prefix (https://) or auth injection (SSH socket, HTTP token).
//
// When resolution changes the receiver (e.g., "github.com/foo" → "https://github.com/foo"),
// we must "redirect" the entire call chain to the corrected receiver so the cache keys match.
// This is similar to an HTTP redirect: same request, different base URL.
//
// Example:
//
//	Original:  git("github.com/foo").branch("main").tree()
//	Resolved:  git("https://github.com/foo").branch("main").tree()
//
// The redirect* helpers rebuild and load the call chain on the resolved receiver.

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// alreadyResolving returns true if we're inside a redirected __resolve call.
// When redirect() loads the new ID, it triggers __resolve again. This check
// prevents infinite recursion by detecting that re-entry.
func alreadyResolving(id *call.ID) bool {
	return id.Field() == "__resolve"
}

// receiverChanged returns true if resolution produced a different receiver.
func receiverChanged[T dagql.Typed](resolved, original dagql.ObjectResult[T]) bool {
	return resolved.ID().Digest() != original.ID().Digest()
}

// redirect replays the current operation on a resolved receiver (depth 1).
//
// Example: if we're in git("github.com/foo").__resolve and resolve to
// git("https://github.com/foo"), this produces:
//
//	git("https://github.com/foo").__resolve
func redirect[T dagql.Typed](
	ctx context.Context,
	resolvedReceiverID *call.ID,
) (dagql.ObjectResult[T], error) {
	var zero dagql.ObjectResult[T]

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return zero, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	// The operation we're currently in: we replay it on the resolved receiver
	opToReplay := dagql.CurrentID(ctx)

	redirectedID := resolvedReceiverID.Append(
		opToReplay.Type().ToAST(),
		opToReplay.Field(),
		call.WithArgs(opToReplay.Args()...),
		call.WithView(opToReplay.View()),
	)

	result, err := srv.Load(ctx, redirectedID)
	if err != nil {
		return zero, err
	}
	return result.(dagql.ObjectResult[T]), nil
}

// redirectScalar is like redirect but for scalar return types (e.g., String, Array).
func redirectScalar[T any](
	ctx context.Context,
	resolvedReceiverID *call.ID,
) (T, error) {
	var zero T

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return zero, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	opToReplay := dagql.CurrentID(ctx)

	redirectedID := resolvedReceiverID.Append(
		opToReplay.Type().ToAST(),
		opToReplay.Field(),
		call.WithArgs(opToReplay.Args()...),
		call.WithView(opToReplay.View()),
	)

	result, err := srv.LoadType(ctx, redirectedID)
	if err != nil {
		return zero, err
	}
	return result.Unwrap().(T), nil
}

// redirectThroughRef replays through a GitRef when the underlying repo changed (depth 2).
// This is Git-specific: it hardcodes the GitRef layer between repo and leaf.
//
// Example:
//
//	Original:  git("github.com/foo").branch("main").__resolve
//	Rebuilt:   git("https://...").branch("main").__resolve
//	               ↑ new receiver   ↑ ref to replay  ↑ leaf to replay
func redirectThroughRef[T dagql.Typed](
	ctx context.Context,
	resolvedRepoID *call.ID,
	refOpToReplay *call.ID,
) (dagql.ObjectResult[T], error) {
	var zero dagql.ObjectResult[T]

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return zero, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	leafOpToReplay := dagql.CurrentID(ctx)

	// Replay ref: resolvedRepo.branch("main")
	rebuiltRefID := resolvedRepoID.Append(
		(*core.GitRef)(nil).Type(),
		refOpToReplay.Field(),
		call.WithArgs(refOpToReplay.Args()...),
		call.WithView(refOpToReplay.View()),
	)

	// Replay leaf: rebuiltRef.__resolve (or .tree(), etc.)
	redirectedID := rebuiltRefID.Append(
		leafOpToReplay.Type().ToAST(),
		leafOpToReplay.Field(),
		call.WithArgs(leafOpToReplay.Args()...),
		call.WithView(refOpToReplay.View()),
	)

	result, err := srv.Load(ctx, redirectedID)
	if err != nil {
		return zero, err
	}
	return result.(dagql.ObjectResult[T]), nil
}

// injectedAuth holds the result of auth detection.
type injectedAuth struct {
	urlOverride string
	extraArgs   []dagql.NamedInput
}
