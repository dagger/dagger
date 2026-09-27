package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// memoTestMod is a stand-in for a non-module Mod (like core), which the memo
// keys by instance.
type memoTestMod struct {
	Mod
	name string
}

func (m *memoTestMod) Name() string { return m.name }

func TestSchemaBuilderMemoized(t *testing.T) {
	a, b := &memoTestMod{name: "a"}, &memoTestMod{name: "b"}
	base := NewSchemaBuilder(nil, []Mod{a})

	t.Run("same entries share a builder", func(t *testing.T) {
		first := base.Clone().Append(b).Memoized()
		require.Same(t, first, base.Clone().Append(b).Memoized())
		require.Same(t, first, base.Append(b).Memoized())
	})

	t.Run("different entries do not", func(t *testing.T) {
		withB := base.Append(b).Memoized()
		require.NotSame(t, withB, base.Clone().Memoized())
		require.NotSame(t, withB, base.Prepend(b).Memoized(), "entry order is part of the key")
		require.NotSame(t, withB, base.Append(&memoTestMod{name: "b"}).Memoized(),
			"a different instance with the same name is a different module")
		require.NotSame(t, withB, base.With(b, InstallOpts{Entrypoint: true}).Memoized(),
			"install options are part of the key")
	})

	t.Run("a failed build is not reused", func(t *testing.T) {
		failed := base.Append(b).Memoized()
		failed.loadSchemaFailed.Store(true)
		replaced := base.Append(b).Memoized()
		require.NotSame(t, failed, replaced)
		require.Same(t, replaced, base.Append(b).Memoized())
	})

	t.Run("a new root does not share the memo", func(t *testing.T) {
		shared := base.Append(b).Memoized()
		require.NotSame(t, shared, base.WithRoot(&Query{}).Append(b).Memoized())
	})
}
