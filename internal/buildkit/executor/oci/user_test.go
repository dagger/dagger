package oci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/sys/user"
	"github.com/stretchr/testify/require"
)

func writeUserFiles(t *testing.T, root, passwd, group string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "etc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "etc", "passwd"), []byte(passwd), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "etc", "group"), []byte(group), 0o644))
}

const (
	testPasswd = "root:x:0:0:root:/root:/bin/sh\nbuilder:x:1000:1000::/home/builder:/bin/sh\n"
	testGroup  = "root:x:0:root\nwheel:x:10:root,builder\nbuilder:x:1000:\n"
)

func TestGetUser(t *testing.T) {
	root := t.TempDir()
	writeUserFiles(t, root, testPasswd, testGroup)

	for range 2 { // parsed, then cached
		uid, gid, sgids, err := GetUser(root, "")
		require.NoError(t, err)
		require.Equal(t, []uint32{0, 0}, []uint32{uid, gid})
		require.Equal(t, []uint32{0, 10}, sgids)

		uid, gid, sgids, err = GetUser(root, "builder")
		require.NoError(t, err)
		require.Equal(t, []uint32{1000, 1000}, []uint32{uid, gid})
		require.Equal(t, []uint32{10}, sgids)

		_, _, _, err = GetUser(root, "nobody")
		require.Error(t, err)
	}

	// A caller may change what it gets without changing what the next gets.
	_, _, sgids, err := GetUser(root, "")
	require.NoError(t, err)
	sgids[0] = 99
	_, _, sgids, err = GetUser(root, "")
	require.NoError(t, err)
	require.Equal(t, []uint32{0, 10}, sgids)
}

// Another image's files give that image's user, cached or not.
func TestGetUserFollowsFiles(t *testing.T) {
	root := t.TempDir()
	writeUserFiles(t, root, testPasswd, testGroup)
	uid, _, _, err := GetUser(root, "builder")
	require.NoError(t, err)
	require.EqualValues(t, 1000, uid)

	writeUserFiles(t, root, strings.ReplaceAll(testPasswd, "1000:1000", "2000:2000"), testGroup)
	uid, gid, _, err := GetUser(root, "builder")
	require.NoError(t, err)
	require.Equal(t, []uint32{2000, 2000}, []uint32{uid, gid})

	writeUserFiles(t, root, testPasswd, strings.ReplaceAll(testGroup, "wheel:x:10:root,builder", "wheel:x:10:root"))
	_, _, sgids, err := GetUser(root, "builder")
	require.NoError(t, err)
	require.Empty(t, sgids)

	// Without a group file, a named user fails and the default user is root,
	// as before the cache.
	require.NoError(t, os.Remove(filepath.Join(root, "etc", "group")))
	_, _, _, err = GetUser(root, "builder")
	require.Error(t, err)
	uid, gid, _, err = GetUser(root, "")
	require.NoError(t, err)
	require.Equal(t, []uint32{0, 0}, []uint32{uid, gid})

	require.NoError(t, os.Remove(filepath.Join(root, "etc", "passwd")))
	uid, gid, sgids, err = GetUser(root, "")
	require.NoError(t, err)
	require.Equal(t, []uint32{0, 0}, []uint32{uid, gid})
	require.Empty(t, sgids)
	_, _, _, err = GetUser(root, "builder")
	require.Error(t, err)
}

// An exec of an image GetUser has seen doesn't parse its files again: that
// parse allocated for every exec whose user isn't a numeric user:group.
func TestGetUserCachedAllocations(t *testing.T) {
	root := t.TempDir()
	var passwd, group strings.Builder
	passwd.WriteString(testPasswd)
	group.WriteString(testGroup)
	for _, name := range []string{"bin", "daemon", "adm", "lp", "sync", "mail", "news", "uucp", "operator", "man"} {
		passwd.WriteString(name + ":x:1:1:" + name + ":/:/sbin/nologin\n")
		group.WriteString(name + ":x:1:root," + name + "\n")
	}
	writeUserFiles(t, root, passwd.String(), group.String())
	_, _, _, err := GetUser(root, "")
	require.NoError(t, err)

	cached := testing.AllocsPerRun(50, func() {
		_, _, _, _ = GetUser(root, "")
	})
	parse := testing.AllocsPerRun(50, func() {
		_, _ = user.GetExecUser("0", nil, strings.NewReader(passwd.String()), strings.NewReader(group.String()))
	})
	require.Less(t, cached, parse/2, "cached lookup %v allocations, parse alone %v", cached, parse)
}
