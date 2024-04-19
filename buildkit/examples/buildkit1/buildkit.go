package main

import (
	"context"
	"flag"
	"os"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/util/system"
)

type buildOpt struct {
	withContainerd bool
	containerd     string
	runc           string
}

func main() {
	var opt buildOpt
	flag.BoolVar(&opt.withContainerd, "with-containerd", true, "enable containerd worker")
	flag.StringVar(&opt.containerd, "containerd", "v1.7.2", "containerd version")
	flag.StringVar(&opt.runc, "runc", "v1.1.7", "runc version")
	flag.Parse()

	bk := buildkit(opt)
	out := bk.Run(llb.Shlex("ls -l /bin")) // debug output

	dt, err := out.Marshal(context.TODO(), llb.LinuxAmd64)
	if err != nil {
		panic(err)
	}
	llb.WriteTo(dt, os.Stdout)
}

func goBuildBase() llb.State {
	goAlpine := llb.Image("docker.io/library/golang:1.21-alpine")
	return goAlpine.
		AddEnv("PATH", "/usr/local/go/bin:"+system.DefaultPathEnvUnix).
		AddEnv("GOPATH", "/go").
		Run(llb.Shlex("apk add --no-cache g++ linux-headers")).
		Run(llb.Shlex("apk add --no-cache git libseccomp-dev make")).Root()
}

func runc(version string) llb.State {
	return goBuildBase().
		With(goFromGit("github.com/opencontainers/runc", version)).
		Run(llb.Shlex("go build -o /usr/bin/runc ./")).
		Root()
}

func containerd(version string) llb.State {
	return goBuildBase().
		Run(llb.Shlex("apk add --no-cache btrfs-progs-dev")).
		With(goFromGit("github.com/containerd/containerd", version)).
		Run(llb.Shlex("make bin/containerd")).Root()
}

func buildkit(opt buildOpt) llb.State {
	src := goBuildBase().With(goFromGit("github.com/moby/buildkit", "master"))

	buildkitd := src.
		Run(llb.Shlex("go build -o /bin/buildkitd ./cmd/buildkitd")).Root()

	buildctl := src.
		Run(llb.Shlex("go build -o /bin/buildctl ./cmd/buildctl")).Root()

	r := llb.Image("docker.io/library/alpine:latest").With(
		copyFrom(buildctl, "/bin/buildctl", "/bin/"),
		copyFrom(buildkitd, "/bin/buildkitd", "/bin/"),
		copyFrom(runc(opt.runc), "/usr/bin/runc", "/bin/"),
	)

	if opt.withContainerd {
		r = r.With(
			copyFrom(containerd(opt.containerd), "/go/src/github.com/containerd/containerd/bin/containerd", "/bin/"),
		)
	}
	return r
}

// goFromGit is a helper for cloning a git repo, checking out a tag and copying
// source directory into
func goFromGit(repo, tag string) llb.StateOption {
	src := llb.Image("docker.io/library/alpine:latest").
		Run(llb.Shlex("apk add --no-cache git")).
		Run(llb.Shlexf("git clone https://%[1]s.git /go/src/%[1]s", repo)).
		Dirf("/go/src/%s", repo).
		Run(llb.Shlexf("git checkout -q %s", tag)).Root()
	return func(s llb.State) llb.State {
		return s.With(copyFrom(src, "/go", "/")).Reset(s).Async(func(ctx context.Context, s llb.State, c *llb.Constraints) (llb.State, error) {
			// TODO: add s.With(s2.DirValue) or s.With(llb.Dir(s2)) or s.Reset(s2, llb.DirMask)?
			dir, err := src.GetDir(ctx)
			if err != nil {
				return llb.State{}, err
			}
			return s.Dir(dir), nil
		})
	}
}

// copyFrom has similar semantics as `COPY --from`
func copyFrom(src llb.State, srcPath, destPath string) llb.StateOption {
	return func(s llb.State) llb.State {
		return copy(src, srcPath, s, destPath)
	}
}

// copy copies files between 2 states using cp until there is no copyOp
func copy(src llb.State, srcPath string, dest llb.State, destPath string) llb.State {
	cpImage := llb.Image("docker.io/library/alpine:latest")
	cp := cpImage.Run(llb.Shlexf("cp -a /src%s /dest%s", srcPath, destPath))
	cp.AddMount("/src", src)
	return cp.AddMount("/dest", dest)
}
