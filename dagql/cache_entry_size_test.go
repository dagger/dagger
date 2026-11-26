package dagql

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// A cache holds an entry for every result it knows, and the Cloud's one for
// every result any engine holds, so an entry's size is paid hundreds of
// thousands of times over. Go gives a heap object over 512 bytes that holds
// pointers an 8-byte header in its size class: an entry of at most 568 bytes
// takes 576, and one of a word more takes 640.
func TestAnEntryFitsGosSizeClassOf576Bytes(t *testing.T) {
	t.Parallel()
	require.LessOrEqual(t, unsafe.Sizeof(sharedResult{}), uintptr(576-8))
}
