package containerd

import (
	"context"
	"testing"
	"time"

	containerdpkg "github.com/containerd/containerd"
)

func GetVersion(t *testing.T, cdAddress string) string {
	t.Helper()

	cdClient, err := containerdpkg.New(cdAddress, containerdpkg.WithTimeout(60*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer cdClient.Close()
	ctx := context.TODO()
	cdVersion, err := cdClient.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return cdVersion.Version
}
