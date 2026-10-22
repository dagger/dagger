package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCacheMountCount(t *testing.T) {
	t.Parallel()
	require.Equal(t, 0, cacheMountCount(nil))
	require.Equal(t, 2, cacheMountCount([]ContainerMount{
		{Target: "/go/pkg/mod", CacheSource: &CacheMountSource{}},
		{Target: "/src", DirectorySource: &LazyAccessor[*Directory, *Container]{}},
		{Target: "/root/.cache/go-build", CacheSource: &CacheMountSource{}, Readonly: true},
		{Target: "/tmp", TmpfsSource: &TmpfsMountSource{}},
	}))
}
