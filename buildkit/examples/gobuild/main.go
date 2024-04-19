//go:build ignore
// +build ignore

package main

import (
	"os"

	"github.com/moby/buildkit/client/llb"
	gobuild "github.com/tonistiigi/llb-gobuild"
)

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

func run() error {
	src := llb.Local("src")

	gb := gobuild.New(nil)
	// gb := gobuild.New(&gobuild.Opt{DevMode: true})

	buildctl, err := gb.BuildExe(gobuild.BuildOpt{
		Source:    src,
		MountPath: "/go/src/github.com/moby/buildkit",
		Pkg:       "github.com/moby/buildkit/cmd/buildctl",
		BuildTags: []string{},
	})
	if err != nil {
		return err
	}

	buildkitd, err := gb.BuildExe(gobuild.BuildOpt{
		Source:    src,
		MountPath: "/go/src/github.com/moby/buildkit",
		Pkg:       "github.com/moby/buildkit/cmd/buildkitd",
		BuildTags: []string{},
	})
	if err != nil {
		return err
	}
	_ = buildkitd

	containerd, err := gb.BuildExe(gobuild.BuildOpt{
		Source:    llb.Git("github.com/containerd/containerd", "v1.2.7"),
		MountPath: "/go/src/github.com/containerd/containerd",
		Pkg:       "github.com/containerd/containerd/cmd/containerd",
		BuildTags: []string{"no_btrfs"},
	})
	if err != nil {
		return err
	}
	runc, err := gb.BuildExe(gobuild.BuildOpt{
		CgoEnabled: true,
		Source:     llb.Git("github.com/opencontainers/runc", "master"),
		MountPath:  "/go/src/github.com/opencontainers/runc",
		Pkg:        "github.com/opencontainers/runc",
		BuildTags:  []string{},
	})
	if err != nil {
		return err
	}

	sc := llb.Scratch().
		With(copyAll(*buildctl, "/")).
		With(copyAll(*containerd, "/")).
		// With(copyAll(*buildkitd, "/")).
		With(copyAll(*runc, "/"))

	dt, err := sc.Marshal(llb.LinuxAmd64)
	if err != nil {
		panic(err)
	}
	llb.WriteTo(dt, os.Stdout)
	return nil
}

func copyAll(src llb.State, destPath string) llb.StateOption {
	return copyFrom(src, "/.", destPath)
}

// copyFrom has similar semantics as `COPY --from`
func copyFrom(src llb.State, srcPath, destPath string) llb.StateOption {
	return func(s llb.State) llb.State {
		return copy(src, srcPath, s, destPath)
	}
}

// copy copies files between 2 states using cp until there is no copyOp
func copy(src llb.State, srcPath string, dest llb.State, destPath string) llb.State {
	cpImage := llb.Image("docker.io/library/alpine@sha256:1072e499f3f655a032e88542330cf75b02e7bdf673278f701d7ba61629ee3ebe")
	cp := cpImage.Run(llb.Shlexf("cp -a /src%s /dest%s", srcPath, destPath))
	cp.AddMount("/src", src, llb.Readonly)
	return cp.AddMount("/dest", dest)
}
