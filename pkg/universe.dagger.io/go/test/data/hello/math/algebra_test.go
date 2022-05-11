package math_test

import (
	"testing"

	"dagger.io/testgreet/internal/testutil"
	"dagger.io/testgreet/math"
)

func TestAdd(t *testing.T) {
	got := math.Add(1, 2)
	if got != 3 {
		t.Fatalf("expected 3, exected %d", got)
	}

	err := testutil.OKResultFile("/tmp/math_test.result")
	if err != nil {
		t.Fatalf("can not create test result file: %v", err)
	}
}
