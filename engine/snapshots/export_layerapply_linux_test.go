package snapshots

import (
	"os"
	"sync/atomic"
)

// CountLayerLookups counts the layer extraction's lookups: Lstats of a
// path, and full resolutions of a path inside its root, until restore.
// Tests that count don't run in parallel.
func CountLayerLookups() (lstats, resolutions *atomic.Int64, restore func()) {
	lstats, resolutions = &atomic.Int64{}, &atomic.Int64{}
	lstat, rootPath := layerLstat, layerRootPath
	layerLstat = func(name string) (os.FileInfo, error) {
		lstats.Add(1)
		return lstat(name)
	}
	layerRootPath = func(root, path string) (string, error) {
		resolutions.Add(1)
		return rootPath(root, path)
	}
	return lstats, resolutions, func() { layerLstat, layerRootPath = lstat, rootPath }
}
