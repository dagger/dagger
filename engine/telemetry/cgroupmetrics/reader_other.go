//go:build !linux

package cgroupmetrics

import "errors"

const (
	defaultProcRoot   = ""
	defaultCgroupRoot = ""
)

var errUnsupportedPlatform = errors.New("cgroup v2 metrics are not supported on this platform")

type reader struct {
	dir   string
	inode uint64
}

func newReader(_, _ string) (*reader, error) {
	return nil, errUnsupportedPlatform
}

func (r *reader) read(string) ([]byte, error) {
	return nil, errUnsupportedPlatform
}
