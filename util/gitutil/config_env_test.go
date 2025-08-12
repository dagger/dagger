package gitutil

import (
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeGitConfigEnv_AppendsAfterMaxIndexOrCount(t *testing.T) {
	env := []string{
		"FOO=bar",
		"GIT_CONFIG_KEY_7=core.abbrev",
		"GIT_CONFIG_VALUE_7=12",
		"GIT_CONFIG_COUNT=5",
		"OTHER=keepme",
	}
	add := map[string]string{
		"http.https://host/.extraheader": "Authorization: basic abc",
		"core.autocrlf":                  "false",
	}
	got := MergeGitConfigEnv(env, add)

	idx := map[string]int{}
	for _, e := range got {
		if strings.HasPrefix(e, "GIT_CONFIG_KEY_") {
			parts := strings.SplitN(e, "=", 2)
			n, _ := strconv.Atoi(strings.TrimPrefix(parts[0], "GIT_CONFIG_KEY_"))
			idx[parts[1]] = n
		}
	}

	require.Contains(t, idx, "http.https://host/.extraheader")
	require.Contains(t, idx, "core.autocrlf")

	gotIdx := []int{idx["http.https://host/.extraheader"], idx["core.autocrlf"]}
	sort.Ints(gotIdx)
	require.Equal(t, []int{8, 9}, gotIdx)

	require.Equal(t, "GIT_CONFIG_COUNT=10", got[len(got)-1])
}

func TestMergeGitConfigEnv_DeterministicOrder(t *testing.T) {
	env := []string{}
	add := map[string]string{
		"b.key": "2",
		"a.key": "1",
	}
	got := MergeGitConfigEnv(env, add)
	idxA, idxB := -1, -1
	for _, e := range got {
		if strings.HasPrefix(e, "GIT_CONFIG_KEY_") && strings.HasSuffix(e, "=a.key") {
			idxA, _ = strconv.Atoi(strings.TrimPrefix(strings.SplitN(e, "=", 2)[0], "GIT_CONFIG_KEY_"))
		}
		if strings.HasPrefix(e, "GIT_CONFIG_KEY_") && strings.HasSuffix(e, "=b.key") {
			idxB, _ = strconv.Atoi(strings.TrimPrefix(strings.SplitN(e, "=", 2)[0], "GIT_CONFIG_KEY_"))
		}
	}
	require.NotEqual(t, -1, idxA)
	require.NotEqual(t, -1, idxB)
	require.Less(t, idxA, idxB, "keys must be appended in sorted order (a before b)")
}
