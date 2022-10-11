package project

import (
	"context"
	"fmt"

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"go.dagger.io/dagger/core"
)

// TODO:(sipsma) SDKs should be pluggable extensions, not hardcoded LLB here. The implementation here is a temporary bridge from the previous hardcoded Dockerfiles to the sdk-as-extension model.

// return the FS with the executable extension code, ready to be invoked by dagger
func (p *State) Runtime(ctx context.Context, ext *Extension, gw bkgw.Client, platform specs.Platform, sshAuthSockID string) (*core.Directory, error) {
	var runtimeFS *core.Directory
	var err error
	switch ext.SDK {
	case "go":
		runtimeFS, err = p.goRuntime(ctx, ext.Path, gw, platform)
	case "ts":
		runtimeFS, err = p.tsRuntime(ctx, ext.Path, gw, platform, sshAuthSockID)
	case "python":
		runtimeFS, err = p.pythonRuntime(ctx, ext.Path, gw, platform, sshAuthSockID)
	case "dockerfile":
		runtimeFS, err = p.dockerfileRuntime(ctx, ext.Path, gw, platform)
	default:
		return nil, fmt.Errorf("unknown sdk %q", ext.SDK)
	}
	if err != nil {
		return nil, err
	}
	if _, err := runtimeFS.Stat(ctx, gw, "."); err != nil {
		return nil, err
	}
	return runtimeFS, nil
}

// return the project filesystem plus any generated code from the SDKs of the extensions and scripts in the project
func (p *State) Generate(ctx context.Context, coreSchema string, gw bkgw.Client, platform specs.Platform, sshAuthSockID string) (*core.Directory, error) {
	var generatedFSes []*core.Directory
	extensions, err := p.Extensions(ctx, gw, platform, sshAuthSockID)
	if err != nil {
		return nil, err
	}
	for _, ext := range extensions {
		switch ext.SDK {
		case "go":
			generatedFS, err := p.goGenerate(ctx, ext.Path, ext.Schema, coreSchema, gw, platform)
			if err != nil {
				return nil, err
			}
			diff, err := p.workdir.Diff(ctx, generatedFS)
			if err != nil {
				return nil, err
			}
			generatedFSes = append(generatedFSes, diff)
		default:
			fmt.Printf("unsupported sdk for generation %q\n", ext.SDK)
		}
	}
	for _, script := range p.Scripts() {
		switch script.SDK {
		case "go":
			generatedFS, err := p.goGenerate(ctx, script.Path, "", coreSchema, gw, platform)
			if err != nil {
				return nil, err
			}
			diff, err := p.workdir.Diff(ctx, generatedFS)
			if err != nil {
				return nil, err
			}
			generatedFSes = append(generatedFSes, diff)
		default:
			fmt.Printf("unsupported sdk for generation %q\n", script.SDK)
		}
	}
	return core.MergeDirectories(ctx, generatedFSes, platform)
}
