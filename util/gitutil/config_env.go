package gitutil

import (
	"maps"
	"slices"
	"strconv"
	"strings"
)

// MergeGitConfigEnv appends git config entries to the environment using
// GIT_CONFIG_COUNT/KEY_i/VALUE_i. It preserves existing env vars (except it
// drops any prior COUNT lines), appends new pairs in sorted key order for
// determinism, and writes exactly one final COUNT.
//
// It defensively scans both the current COUNT and the highest existing KEY_/VALUE_
// index, then appends after max(COUNT, maxIndex+1).
func MergeGitConfigEnv(env []string, entries map[string]string) []string {
	if len(entries) == 0 {
		return env
	}

	const (
		countPrefix = "GIT_CONFIG_COUNT="
		keyPrefix   = "GIT_CONFIG_KEY_"
		valPrefix   = "GIT_CONFIG_VALUE_"
	)

	maxCount := 0
	maxIdx := -1
	out := make([]string, 0, len(env)+len(entries)*2+1)

	for _, e := range env {
		if s, ok := strings.CutPrefix(e, countPrefix); ok {
			if n, err := strconv.Atoi(s); err == nil && n > maxCount {
				maxCount = n
			}
			continue
		}
		if s, ok := strings.CutPrefix(e, keyPrefix); ok {
			head, _, _ := strings.Cut(s, "=")
			if n, err := strconv.Atoi(head); err == nil && n > maxIdx {
				maxIdx = n
			}
		} else if s, ok := strings.CutPrefix(e, valPrefix); ok {
			head, _, _ := strings.Cut(s, "=")
			if n, err := strconv.Atoi(head); err == nil && n > maxIdx {
				maxIdx = n
			}
		}
		out = append(out, e)
	}

	next := maxCount
	if maxIdx+1 > next {
		next = maxIdx + 1
	}

	keys := slices.Sorted(maps.Keys(entries))
	for i, k := range keys {
		v := entries[k]
		idx := next + i
		out = append(out,
			"GIT_CONFIG_KEY_"+strconv.Itoa(idx)+"="+k,
			"GIT_CONFIG_VALUE_"+strconv.Itoa(idx)+"="+v,
		)
	}

	out = append(out, countPrefix+strconv.Itoa(next+len(keys)))
	return out
}
