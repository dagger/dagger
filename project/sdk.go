package project

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// TODO:(sipsma) SDKs should be pluggable extensions, not hardcoded LLB here. The implementation here is a temporary bridge from the previous hardcoded Dockerfiles to the sdk-as-extension model.

// return the FS with the executable extension code, ready to be invoked by dagger
func (p *State) Runtime(ctx context.Context, session *core.Session, platform specs.Platform, sshAuthSockID string) (*core.Directory, error) {
	var runtimeFS *core.Directory
	var err error
	switch p.config.SDK {
	case "go":
		runtimeFS, err = p.goRuntime(ctx, "/", session, platform)
	case "ts":
		runtimeFS, err = p.tsRuntime(ctx, "/", session, platform, sshAuthSockID)
	case "python":
		runtimeFS, err = p.pythonRuntime(ctx, "/", session, platform, sshAuthSockID)
	case "dockerfile":
		runtimeFS, err = p.dockerfileRuntime(ctx, "/", session, platform)
	default:
		return nil, fmt.Errorf("unknown sdk %q", p.config.SDK)
	}
	if err != nil {
		return nil, err
	}
	if _, err := runtimeFS.Stat(ctx, session, "."); err != nil {
		return nil, err
	}
	return runtimeFS, nil
}
