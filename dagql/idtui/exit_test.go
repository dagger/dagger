package idtui

import (
	"errors"
	"testing"
)

func TestExitErrorCode(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		err  ExitError
		want int
	}{
		{
			name: "zero code without error stays zero",
			err:  ExitError{},
			want: 0,
		},
		{
			name: "nonzero code is preserved",
			err:  ExitError{OriginalCode: 42, Original: errors.New("boom")},
			want: 42,
		},
		{
			name: "error with zero code coerces to one",
			err:  ExitError{Original: errors.New("boom")},
			want: 1,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.err.Code(); got != tc.want {
				t.Fatalf("ExitError{OriginalCode: %d, Original: %v}.Code() = %d, want %d", tc.err.OriginalCode, tc.err.Original, got, tc.want)
			}
		})
	}
}
