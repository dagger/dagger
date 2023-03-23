package main

import "golang.org/x/exp/constraints"

func max[T constraints.Ordered](i, j T) T {
	if i > j {
		return i
	}
	return j
}

func min[T constraints.Ordered](a, b T) T {
	if a < b {
		return a
	}
	return b
}
