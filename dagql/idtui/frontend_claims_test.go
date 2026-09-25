package idtui

import "testing"

// TestRenderClaimsFork covers the forked-claims transaction: a fork dedupes
// against what's already claimed, keeps its own claims separate until commit,
// and a discarded fork leaves the pass untouched.
func TestRenderClaimsFork(t *testing.T) {
	seeded, logged, owned := prettyTestSpanID(1), prettyTestSpanID(2), prettyTestSpanID(3)

	pass := newRenderClaims()
	pass.claimTestCase(seeded)
	pass.claimLogID(logged)

	fork := pass.fork()
	if !fork.hasTestCase(seeded) || !fork.hasLog(logged) {
		t.Fatal("a fork must see claims made before it")
	}
	if fork.ownsTestCase(seeded) || fork.ownsAnyTestCases() {
		t.Fatal("a fork must not own claims made before it")
	}

	fork.claimTestCase(owned)
	if !fork.ownsTestCase(owned) {
		t.Fatal("a fork must own the claims it makes")
	}
	if pass.hasTestCase(owned) {
		t.Fatal("a fork's claims must not reach the pass before commit")
	}

	discarded := pass.fork()
	discarded.claimLogID(owned)
	if pass.hasLog(owned) {
		t.Fatal("an uncommitted fork must leave the pass untouched")
	}

	fork.commit()
	if !pass.hasTestCase(owned) {
		t.Fatal("commit must fold a fork's claims into the pass")
	}
}
