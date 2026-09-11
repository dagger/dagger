package core

// Regression net for the lazy re-forcing hazard behind
// hack/designs/async-agents.md §9. Dang forces a lazy chain once per
// SELECTION off it, not once per `let` binding, so a `let` over an
// object-returning call that dagql cannot deduplicate — a module function
// with @cache(policy: Never) — re-runs the call, side effects and all, for
// every field the body reads back. §9's "the side effect executes exactly
// once" holds only for the ID-returning verbs, which Dang forces at the call
// site and re-hydrates through node(id:).
//
// modules/staff's harvest tools rest on that escape hatch (workerWorkspace
// pins the worker's DoNotCache snapshot with LLM.sync), so both halves are
// measured here: the hazard, and the pin that dodges it.

import (
	"context"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type DangForcingSuite struct{}

func TestDangForcing(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(DangForcingSuite{})
}

// TestLazyChainForcing counts agent instances minted behind one `let`. The
// fixture's witness is LLM.spawn: DoNotCache, and a fresh instance per
// evaluation, so the number of distinct handles two reads observe is the
// number of times the chain ran.
func (DangForcingSuite) TestLazyChainForcing(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	mod := dangModule(t, c, "lazy-forcing")

	// The hazard, pinned down so a change in Dang's forcing semantics shows
	// up here rather than as duplicate work in a module.
	out, err := mod.With(daggerCall("unpinned-instances")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "2", strings.TrimSpace(out),
		"two selections off an unpinned @cache(Never) chain must re-execute it")

	// The escape hatch modules/staff depends on.
	out, err = mod.With(daggerCall("pinned-instances")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "1", strings.TrimSpace(out),
		"an ID-returning verb is forced at the call site; re-selection must replay the lookup, not the mint")
}
