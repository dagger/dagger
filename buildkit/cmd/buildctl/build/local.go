package build

import (
	"github.com/pkg/errors"
	"github.com/tonistiigi/fsutil"
)

// ParseLocal parses --local
func ParseLocal(locals []string) (map[string]fsutil.FS, error) {
	localDirs, err := attrMap(locals)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	mounts := make(map[string]fsutil.FS, len(localDirs))

	for k, v := range localDirs {
		mounts[k], err = fsutil.NewFS(v)
		if err != nil {
			return nil, errors.WithStack(err)
		}
	}

	return mounts, nil
}
