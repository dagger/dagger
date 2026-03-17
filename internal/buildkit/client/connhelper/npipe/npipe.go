// Package npipe provides connhelper for npipe://<address>
package npipe

import "github.com/dagger/dagger/internal/buildkit/client/connhelper"

func init() {
	connhelper.Register("npipe", Helper)
}
