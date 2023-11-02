package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOriginToPath(t *testing.T) {
	for _, tc := range []struct {
		origin string
		want   string
	}{
		{
			origin: "ssh://git@github.com/shykes/daggerverse",
			want:   "github.com/shykes/daggerverse",
		},
		{
			origin: "ssh://git@github.com/shykes/daggerverse.git",
			want:   "github.com/shykes/daggerverse",
		},
		{
			origin: "git@github.com:sipsma/daggerverse",
			want:   "github.com/sipsma/daggerverse",
		},
		{
			origin: "git@github.com:sipsma/daggerverse.git",
			want:   "github.com/sipsma/daggerverse",
		},
		{
			origin: "https://github.com/sipsma/daggerverse",
			want:   "github.com/sipsma/daggerverse",
		},
		{
			origin: "https://github.com/sipsma/daggerverse.git",
			want:   "github.com/sipsma/daggerverse",
		},
	} {
		p, err := originToPath(tc.origin)
		require.NoError(t, err)
		require.Equal(t, tc.want, p)
	}
}
