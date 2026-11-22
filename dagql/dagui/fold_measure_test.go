package dagui

import (
	"testing"

	"github.com/dagger/dagger/internal/spanfootprint"
)

// TestFoldBreakdown checks the breakdown accounts for every retained span,
// and that simulating the policy as built retains what the fold did.
func TestFoldBreakdown(t *testing.T) {
	for _, nested := range []bool{false, true} {
		tr := spanfootprint.New(30_000)
		folded, store := newFoldedDB(nil)
		exp := tr.WaitChains(store)
		if nested {
			exp = spanfootprint.Nested(exp)
		}
		if err := tr.Export(t.Context(), exp, 512, nil); err != nil {
			t.Fatal(err)
		}
		b := folded.fold.Breakdown()
		var sum int
		for _, r := range b.Reasons {
			sum += r.Spans
		}
		if sum != b.Retained {
			t.Fatalf("reasons sum to %d, retained %d", sum, b.Retained)
		}
		asBuilt := b.WhatIf[0].Spans
		if diff := asBuilt - b.Retained; diff < -b.Retained/100 || diff > b.Retained/100 {
			t.Fatalf("simulated %d retained, the fold retained %d", asBuilt, b.Retained)
		}
		t.Logf("nested=%v: %d/%d retained, reasons %v, what-if %v, folded calls %d", nested, b.Retained, b.Spans, b.Reasons, b.WhatIf, b.FoldedCalls)
	}
}
