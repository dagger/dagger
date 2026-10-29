//go:build !linux

package wcbpf

// WatchExec measures nothing outside Linux.
func WatchExec(string, bool) *Exec {
	return nil
}
