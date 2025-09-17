package gitutil

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const keyPrefix = "GIT_CONFIG_KEY_"

func parseKeyLine(e string) (idx int, key string, ok bool) {
	rest, found := strings.CutPrefix(e, keyPrefix)
	if !found {
		return 0, "", false
	}
	head, val, found := strings.Cut(rest, "=")
	if !found {
		return 0, "", false
	}
	n, err := strconv.Atoi(head)
	if err != nil {
		return 0, "", false
	}
	return n, val, true
}

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
		if n, key, ok := parseKeyLine(e); ok {
			idx[key] = n
		}
	}

	require.Contains(t, idx, "http.https://host/.extraheader")
	require.Contains(t, idx, "core.autocrlf")

	gotIdx := []int{idx["http.https://host/.extraheader"], idx["core.autocrlf"]}
	slices.Sort(gotIdx)
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
		if n, key, ok := parseKeyLine(e); ok {
			switch key {
			case "a.key":
				idxA = n
			case "b.key":
				idxB = n
			}
		}
	}

	require.NotEqual(t, -1, idxA)
	require.NotEqual(t, -1, idxB)
	require.Less(t, idxA, idxB, "keys must be appended in sorted order (a before b)")
}
