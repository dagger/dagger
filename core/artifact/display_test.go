package artifact

import (
	"testing"

	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/stretchr/testify/require"
)

func TestVisibleDimensions(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows [][]flattening.Coordinate
		want []string
	}{
		{name: "empty"},
		{name: "single", rows: [][]flattening.Coordinate{{{Dimension: "x", Key: "a"}}}},
		{name: "all missing", rows: [][]flattening.Coordinate{nil, nil}},
		{name: "equal", rows: [][]flattening.Coordinate{{{Dimension: "x", Key: "a"}}, {{Dimension: "x", Key: "a"}}}},
		{name: "missing and empty", rows: [][]flattening.Coordinate{nil, {{Dimension: "x", Key: ""}}}, want: []string{"x"}},
		{name: "mixed presence", rows: [][]flattening.Coordinate{{{Dimension: "x", Key: "a"}}, nil, {{Dimension: "x", Key: "a"}}}, want: []string{"x"}},
		{name: "different", rows: [][]flattening.Coordinate{{{Dimension: "x", Key: "a"}}, {{Dimension: "x", Key: "b"}}}, want: []string{"x"}},
		{name: "repeated equal sets", rows: [][]flattening.Coordinate{{{Dimension: "x", Key: "a"}, {Dimension: "x", Key: "b"}}, {{Dimension: "x", Key: "b"}, {Dimension: "x", Key: "a"}, {Dimension: "x", Key: "a"}}}},
		{name: "repeated different sets", rows: [][]flattening.Coordinate{{{Dimension: "x", Key: "a"}, {Dimension: "x", Key: "b"}}, {{Dimension: "x", Key: "a"}}}, want: []string{"x"}},
		{name: "sorted columns", rows: [][]flattening.Coordinate{{{Dimension: "z", Key: "1"}, {Dimension: "a", Key: "1"}}, nil}, want: []string{"a", "z"}},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, append([]string{}, tc.want...), VisibleDimensions(tc.rows)) })
	}
}
