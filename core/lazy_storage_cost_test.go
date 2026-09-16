package core

import (
	"testing"
	"unsafe"
)

func TestLazyStorageLayout(t *testing.T) {
	t.Logf("bytes LazyState=%d Directory=%d File=%d Container=%d scratch operation=%d mount Directory operation=%d mount File operation=%d builtin operation=%d", unsafe.Sizeof(LazyState{}), unsafe.Sizeof(Directory{}), unsafe.Sizeof(File{}), unsafe.Sizeof(Container{}), unsafe.Sizeof(DirectoryScratchLazy{}), unsafe.Sizeof(ContainerWithMountedDirectoryLazy{}), unsafe.Sizeof(ContainerWithMountedFileLazy{}), unsafe.Sizeof(ContainerBuiltinLazy{}))
}
