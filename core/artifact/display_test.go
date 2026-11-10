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
		{name: "single", rows: [][]flattening.Coordinate{{{"x", "a"}}}},
		{name: "all missing", rows: [][]flattening.Coordinate{nil, nil}},
		{name: "equal", rows: [][]flattening.Coordinate{{{"x", "a"}}, {{"x", "a"}}}},
		{name: "missing and empty", rows: [][]flattening.Coordinate{nil, {{"x", ""}}}, want: []string{"x"}},
		{name: "mixed presence", rows: [][]flattening.Coordinate{{{"x", "a"}}, nil, {{"x", "a"}}}, want: []string{"x"}},
		{name: "different", rows: [][]flattening.Coordinate{{{"x", "a"}}, {{"x", "b"}}}, want: []string{"x"}},
		{name: "repeated equal sets", rows: [][]flattening.Coordinate{{{"x", "a"}, {"x", "b"}}, {{"x", "b"}, {"x", "a"}, {"x", "a"}}}},
		{name: "repeated different sets", rows: [][]flattening.Coordinate{{{"x", "a"}, {"x", "b"}}, {{"x", "a"}}}, want: []string{"x"}},
		{name: "sorted columns", rows: [][]flattening.Coordinate{{{"z", "1"}, {"a", "1"}}, nil}, want: []string{"a", "z"}},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, append([]string{}, tc.want...), VisibleDimensions(tc.rows)) })
	}
}
