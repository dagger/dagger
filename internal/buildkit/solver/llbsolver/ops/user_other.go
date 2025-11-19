//go:build !linux && !windows
// +build !linux,!windows

package ops

import (
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/worker"
	"github.com/pkg/errors"
	copy "github.com/dagger/dagger/internal/fsutil/copy"
)

func getReadUserFn(_ worker.Worker) func(chopt *pb.ChownOpt, mu, mg snapshot.Mountable) (*copy.User, error) {
	return readUser
}

func readUser(chopt *pb.ChownOpt, mu, mg snapshot.Mountable) (*copy.User, error) {
	if chopt == nil {
		return nil, nil
	}
	return nil, errors.New("only implemented in linux and windows")
}
