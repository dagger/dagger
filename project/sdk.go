package project

import (
	"context"
	"fmt"

	"github.com/dagger/cloak/core/filesystem"
)

// TODO:(sipsma) SDKs should be pluggable extensions, not hardcoded LLB here. The implementation here is a temporary bridge from the previous hardcoded Dockerfiles to the sdk-as-extension model.

// return the FS with the executable extension code, ready to be invoked by cloak
func (s RemoteSchema) Runtime(ctx context.Context, ext *Extension) (*filesystem.Filesystem, error) {
	var runtimeFS *filesystem.Filesystem
	var err error
	switch ext.SDK {
	case "go":
		runtimeFS, err = s.goRuntime(ctx, ext.Path)
	case "ts":
		runtimeFS, err = s.tsRuntime(ctx, ext.Path)
	case "dockerfile":
		runtimeFS, err = s.dockerfileRuntime(ctx, ext.Path)
	default:
		return nil, fmt.Errorf("unknown sdk %q", ext.SDK)
	}
	if err != nil {
		return nil, err
	}
	if err := runtimeFS.Evaluate(ctx, s.gw); err != nil {
		return nil, err
	}
	return runtimeFS, nil
}

// return the project filesystem plus any generated code from the SDKs of the extensions and scripts in the project
func (s RemoteSchema) Generate(ctx context.Context, coreSchema string) (*filesystem.Filesystem, error) {
	var generatedFSes []*filesystem.Filesystem
	for _, ext := range s.extensions {
		switch ext.SDK {
		case "go":
			generatedFS, err := s.goGenerate(ctx, ext.Path, ext.Schema, coreSchema)
			if err != nil {
				return nil, err
			}
			diff, err := filesystem.Diffed(ctx, s.contextFS, generatedFS, s.platform)
			if err != nil {
				return nil, err
			}
			generatedFSes = append(generatedFSes, diff)
		default:
			fmt.Printf("unsupported sdk for generation %q\n", ext.SDK)
		}
	}
	for _, script := range s.scripts {
		switch script.SDK {
		case "go":
			generatedFS, err := s.goGenerate(ctx, script.Path, "", coreSchema)
			if err != nil {
				return nil, err
			}
			diff, err := filesystem.Diffed(ctx, s.contextFS, generatedFS, s.platform)
			if err != nil {
				return nil, err
			}
			generatedFSes = append(generatedFSes, diff)
		default:
			fmt.Printf("unsupported sdk for generation %q\n", script.SDK)
		}
	}
	return filesystem.Merged(ctx, generatedFSes, s.platform)
}
