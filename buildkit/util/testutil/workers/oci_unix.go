//go:build !windows
// +build !windows

package workers

import (
	"fmt"
	"os"

	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/testutil/integration"
)

func initOCIWorker() {
	integration.Register(&OCI{ID: "oci"})

	// the rootless uid is defined in Dockerfile
	if s := os.Getenv("BUILDKIT_INTEGRATION_ROOTLESS_IDPAIR"); s != "" {
		var uid, gid int
		if _, err := fmt.Sscanf(s, "%d:%d", &uid, &gid); err != nil {
			bklog.L.Fatalf("unexpected BUILDKIT_INTEGRATION_ROOTLESS_IDPAIR: %q", s)
		}
		if integration.RootlessSupported(uid) {
			integration.Register(&OCI{ID: "oci-rootless", UID: uid, GID: gid})
			integration.Register(&OCI{ID: "oci-rootless-slirp4netns-detachnetns", UID: uid, GID: gid,
				RootlessKitNet: "slirp4netns", RootlessKitDetachNetNS: true})
		}
	}

	if s := os.Getenv("BUILDKIT_INTEGRATION_SNAPSHOTTER"); s != "" {
		integration.Register(&OCI{ID: "oci-snapshotter-" + s, Snapshotter: s})
	}
}
