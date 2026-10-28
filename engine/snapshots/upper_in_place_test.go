package snapshots

import (
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/stretchr/testify/require"
)

type staticMounts []mount.Mount

func (m staticMounts) Mount() ([]mount.Mount, func() error, error) {
	return m, func() error { return nil }, nil
}

// A snapshot prepared on the empty scratch snapshot is mounted from its upper
// directory, without an overlay, only when the overlay's one lower layer is
// that empty snapshot.
func TestUpperInPlace(t *testing.T) {
	overlay := func(lower string) mount.Mount {
		return mount.Mount{Type: "overlay", Source: "overlay", Options: []string{
			"index=off", "lowerdir=" + lower, "upperdir=/s/2/fs", "workdir=/s/2/work",
		}}
	}
	for _, tc := range []struct {
		name string
		in   []mount.Mount
		want []mount.Mount
	}{
		{"one lower", []mount.Mount{overlay("/s/1/fs")},
			[]mount.Mount{{Type: "bind", Source: "/s/2/fs", Options: []string{"rbind"}}}},
		{"two lowers", []mount.Mount{overlay("/s/1/fs:/s/0/fs")}, []mount.Mount{overlay("/s/1/fs:/s/0/fs")}},
		{"bind", []mount.Mount{{Type: "bind", Source: "/s/2/fs", Options: []string{"rbind"}}},
			[]mount.Mount{{Type: "bind", Source: "/s/2/fs", Options: []string{"rbind"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, release, err := upperInPlace{staticMounts(tc.in)}.Mount()
			require.NoError(t, err)
			require.NoError(t, release())
			require.Equal(t, tc.want, got)
		})
	}
}
