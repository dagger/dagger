package math_test

import (
	"testing"

	"dagger.io/testgreet/math"
)

func TestAdd(t *testing.T) {
	got := math.Add(1, 2)
	if got != 3 {
		t.Fatalf("expected 3, exected %d", got)
	}
}
