package main

import "os"

type Goproxy struct{}

// Value returns GOPROXY as this module's runtime sees it. The runtime runs in
// the container that built it, with the same inherited system env.
func (m *Goproxy) Value() string {
	return os.Getenv("GOPROXY")
}
