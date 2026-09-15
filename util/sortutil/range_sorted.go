package sortutil

import (
	"cmp"
	"slices"
)

func RangeSorted[K cmp.Ordered, V any](m map[K]V, cb func(k K, v V)) {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		cb(k, m[k])
	}
}
