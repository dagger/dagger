package progressui

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func mkinterval(start, stop int64) interval {
	unixStart := time.Unix(start, 0)
	unixStop := time.Unix(stop, 0)
	return interval{start: &unixStart, stop: &unixStop}
}

func mkOpenInterval(start int64) interval {
	unixStart := time.Unix(start, 0)
	return interval{start: &unixStart, stop: nil}
}

func TestMergeIntervals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		input    []interval
		expected []interval
	}{
		{
			name:     "none",
			input:    nil,
			expected: nil,
		},
		{
			name: "one",
			input: []interval{
				mkinterval(0, 1),
			},
			expected: []interval{
				mkinterval(0, 1),
			},
		},
		{
			name: "unstarted",
			input: []interval{
				mkinterval(0, 1),
				{nil, nil},
			},
			expected: []interval{
				mkinterval(0, 1),
			},
		},
		{
			name: "equal",
			input: []interval{
				mkinterval(2, 4),
				mkinterval(2, 4),
			},
			expected: []interval{
				mkinterval(2, 4),
			},
		},
		{
			name: "no overlap",
			input: []interval{
				mkinterval(0, 1),
				mkinterval(2, 3),
				mkinterval(7, 8),
			},
			expected: []interval{
				mkinterval(0, 1),
				mkinterval(2, 3),
				mkinterval(7, 8),
			},
		},
		{
			name: "subsumed",
			input: []interval{
				mkinterval(0, 10),
				mkinterval(1, 2),
				mkinterval(4, 9),
				mkinterval(9, 10),
			},
			expected: []interval{
				mkinterval(0, 10),
			},
		},
		{
			name: "partial overlaps",
			input: []interval{
				mkinterval(0, 3),
				mkinterval(2, 5),
				mkinterval(4, 8),
				mkinterval(10, 12),
				mkinterval(11, 14),
			},
			expected: []interval{
				mkinterval(0, 8),
				mkinterval(10, 14),
			},
		},
		{
			name: "joined",
			input: []interval{
				mkinterval(0, 2),
				mkinterval(2, 4),
				mkinterval(4, 6),
				mkinterval(8, 10),
				mkinterval(10, 12),
				mkinterval(11, 12),
				mkinterval(11, 14),
			},
			expected: []interval{
				mkinterval(0, 6),
				mkinterval(8, 14),
			},
		},
		{
			name: "open interval",
			input: []interval{
				mkinterval(0, 5),
				mkOpenInterval(6),
			},
			expected: []interval{
				mkinterval(0, 5),
				mkOpenInterval(6),
			},
		},
		{
			name: "open interval with overlaps",
			input: []interval{
				mkOpenInterval(1),
				mkinterval(3, 5),
			},
			expected: []interval{
				mkOpenInterval(1),
			},
		},
		{
			name: "complex",
			input: []interval{
				mkinterval(0, 2),
				mkinterval(1, 4),
				mkinterval(1, 4),
				mkinterval(1, 5),
				{nil, nil},
				mkinterval(6, 20),
				mkinterval(8, 10),
				mkinterval(8, 10),
				mkinterval(9, 10),
				mkinterval(12, 14),
				mkinterval(19, 21),
				mkinterval(30, 31),
				mkinterval(32, 35),
				{nil, nil},
				mkOpenInterval(33),
			},
			expected: []interval{
				mkinterval(0, 5),
				mkinterval(6, 21),
				mkinterval(30, 31),
				mkOpenInterval(32),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, mergeIntervals(tc.input))
		})
	}
}
