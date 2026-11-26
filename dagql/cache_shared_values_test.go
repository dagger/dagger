package dagql

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type sharedValuesTestValue struct{ text string }

func sharedValuesTestTable() *sharedValues[string, sharedValuesTestValue] {
	return &sharedValues[string, sharedValuesTestValue]{keyOf: func(v *sharedValuesTestValue) string { return v.text }}
}

// A table of shared values hands every holder of a key the one value, and
// forgets the value once no holder points to it: it holds only what is in
// use.
func TestSharedValuesAreForgottenOnceUnused(t *testing.T) {
	t.Parallel()
	table := sharedValuesTestTable()
	made := 0
	value := func(text string) func() *sharedValuesTestValue {
		return func() *sharedValuesTestValue {
			made++
			return &sharedValuesTestValue{text: text}
		}
	}
	func() {
		a := table.share("a", value("a"))
		require.Same(t, a, table.share("a", value("a")))
	}()
	b := table.share("b", value("b"))
	require.Equal(t, 2, made)

	deadline := time.Now().Add(10 * time.Second)
	for table.count() > 1 {
		require.False(t, time.Now().After(deadline), "the table still holds %d values", table.count())
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	require.Same(t, b, table.share("b", value("b")))
	require.Equal(t, 2, made)
}

// A holder that changes a shared value fails the table's check, so that a
// later change that writes to a value its holders share fails the package's
// tests (TestMain).
func TestASharedValueChangedFailsItsCheck(t *testing.T) {
	t.Parallel()
	table := sharedValuesTestTable()
	v := table.share("a", func() *sharedValuesTestValue { return &sharedValuesTestValue{text: "a"} })
	require.NoError(t, table.verify())
	v.text = "changed"
	require.Error(t, table.verify())
	require.Panics(t, func() { table.share("a", func() *sharedValuesTestValue { return &sharedValuesTestValue{text: "a"} }) })
}
