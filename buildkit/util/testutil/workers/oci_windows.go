package workers

import "github.com/moby/buildkit/util/bklog"

func initOCIWorker() {
	bklog.L.Info("OCI Worker not supported on Windows.")
}
