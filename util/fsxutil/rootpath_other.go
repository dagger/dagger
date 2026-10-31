//go:build !linux

package fsxutil

func resolvesWithoutSymlinks(string, bool) bool {
	return false
}
