package workspace

import (
	"strings"
	"testing"

	"github.com/dagger/dagger/util/lockfile"
	"github.com/stretchr/testify/require"
)

func TestModuleResolveSetGetDelete(t *testing.T) {
	lock := NewLock()

	require.NoError(t, lock.SetModuleResolve("github.com/acme/mod@v1.0", "3d23f8", PolicyPin))

	pin, policy, ok := lock.GetModuleResolve("github.com/acme/mod@v1.0")
	require.True(t, ok)
	require.Equal(t, "3d23f8", pin)
	require.Equal(t, PolicyPin, policy)

	require.True(t, lock.DeleteModuleResolve("github.com/acme/mod@v1.0"))
	_, _, ok = lock.GetModuleResolve("github.com/acme/mod@v1.0")
	require.False(t, ok)
}

func TestSetModuleResolvePolicyValidation(t *testing.T) {
	lock := NewLock()

	err := lock.SetModuleResolve("github.com/acme/mod@v1.0", "3d23f8", LockPolicy("weird"))
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid lock policy")
}

func TestParseLockValidation(t *testing.T) {
	t.Run("invalid policy", func(t *testing.T) {
		input := strings.Join([]string{
			`[["version","1"]]`,
			`["","modules.resolve",["github.com/acme/mod@main"],{"value":"123","policy":"weird"}]`,
		}, "\n")

		_, err := ParseLock([]byte(input))
		require.Error(t, err)
		require.ErrorContains(t, err, "invalid modules.resolve entry result")
	})

	t.Run("invalid envelope", func(t *testing.T) {
		input := strings.Join([]string{
			`[["version","1"]]`,
			`["","modules.resolve",["github.com/acme/mod@main"],{"policy":"pin"}]`,
		}, "\n")

		_, err := ParseLock([]byte(input))
		require.Error(t, err)
		require.ErrorContains(t, err, "value is required")
	})
}

func TestPruneModuleResolveEntries(t *testing.T) {
	lock := NewLock()
	require.NoError(t, lock.SetModuleResolve("github.com/acme/a@main", "a1", PolicyPin))
	require.NoError(t, lock.SetModuleResolve("github.com/acme/b@main", "b1", PolicyFloat))
	require.NoError(t, lock.SetModuleResolve("github.com/acme/c@main", "c1", PolicyPin))

	pruned := lock.PruneModuleResolveEntries(map[string]struct{}{
		"github.com/acme/a@main": {},
		"github.com/acme/c@main": {},
	})
	require.Equal(t, 1, pruned)

	_, _, ok := lock.GetModuleResolve("github.com/acme/a@main")
	require.True(t, ok)
	_, _, ok = lock.GetModuleResolve("github.com/acme/b@main")
	require.False(t, ok)
	_, _, ok = lock.GetModuleResolve("github.com/acme/c@main")
	require.True(t, ok)
}

func TestUnknownTuplePreservation(t *testing.T) {
	input := strings.Join([]string{
		`[["version","1"]]`,
		`["","modules.resolve",["github.com/acme/mod@main"],{"value":"111","policy":"pin"}]`,
		`["","git.resolveRef",["https://github.com/acme/ci","refs/heads/main"],{"value":"6e4d","policy":"float"}]`,
		`["github.com/acme/release","lookupVersion",["stable"],{"value":"v1.2.3","policy":"float"}]`,
	}, "\n")

	lock, err := ParseLock([]byte(input))
	require.NoError(t, err)

	pruned := lock.PruneModuleResolveEntries(nil)
	require.Equal(t, 1, pruned)

	output, err := lock.Marshal()
	require.NoError(t, err)

	raw, err := lockfile.Parse(output)
	require.NoError(t, err)

	_, ok := raw.Get("", "git.resolveRef", []any{"https://github.com/acme/ci", "refs/heads/main"})
	require.True(t, ok)
	_, ok = raw.Get("github.com/acme/release", "lookupVersion", []any{"stable"})
	require.True(t, ok)
	_, ok = raw.Get("", "modules.resolve", []any{"github.com/acme/mod@main"})
	require.False(t, ok)
}

func TestLockMarshalDeterministic(t *testing.T) {
	lockA := NewLock()
	require.NoError(t, lockA.SetModuleResolve("github.com/acme/b@main", "b1", PolicyPin))
	require.NoError(t, lockA.SetModuleResolve("github.com/acme/a@main", "a1", PolicyFloat))

	lockB := NewLock()
	require.NoError(t, lockB.SetModuleResolve("github.com/acme/a@main", "a1", PolicyFloat))
	require.NoError(t, lockB.SetModuleResolve("github.com/acme/b@main", "b1", PolicyPin))

	outputA, err := lockA.Marshal()
	require.NoError(t, err)
	outputB, err := lockB.Marshal()
	require.NoError(t, err)

	require.Equal(t, string(outputA), string(outputB))
}

func TestParseLockMode(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		mode, err := ParseLockMode("")
		require.NoError(t, err)
		require.Equal(t, DefaultLockMode, mode)
	})

	t.Run("strict", func(t *testing.T) {
		mode, err := ParseLockMode("strict")
		require.NoError(t, err)
		require.Equal(t, LockModeStrict, mode)
	})

	t.Run("auto", func(t *testing.T) {
		mode, err := ParseLockMode("auto")
		require.NoError(t, err)
		require.Equal(t, LockModeAuto, mode)
	})

	t.Run("update", func(t *testing.T) {
		mode, err := ParseLockMode("update")
		require.NoError(t, err)
		require.Equal(t, LockModeUpdate, mode)
	})

	t.Run("invalid", func(t *testing.T) {
		_, err := ParseLockMode("weird")
		require.Error(t, err)
		require.ErrorContains(t, err, "invalid lock mode")
	})
}
