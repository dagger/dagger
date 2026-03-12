package main

import (
	"errors"
	"testing"
)

func TestNormalizeExitCode(t *testing.T) {
	t.Parallel()

	testErr := errors.New("boom")

	for _, tc := range []struct {
		name string
		code int
		err  error
		want int
	}{
		{name: "zero without error stays zero", code: 0, err: nil, want: 0},
		{name: "nonzero without error stays nonzero", code: 7, err: nil, want: 7},
		{name: "zero with error coerces to one", code: 0, err: testErr, want: 1},
		{name: "nonzero with error stays nonzero", code: 7, err: testErr, want: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeExitCode(tc.code, tc.err); got != tc.want {
				t.Fatalf("normalizeExitCode(%d, %v) = %d, want %d", tc.code, tc.err, got, tc.want)
			}
		})
	}
}
