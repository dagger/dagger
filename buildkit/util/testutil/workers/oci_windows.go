package workers

import "github.com/dagger/dagger/buildkit/util/bklog"

func initOCIWorker() {
	bklog.L.Info("OCI Worker not supported on Windows.")
}
