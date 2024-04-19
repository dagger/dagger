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
		Run(llb.Shlex("apk add --no-cache g++ linux-headers libseccomp-dev make")).Root()
}

func goRepo(s llb.State, repo, ref string, g ...llb.GitOption) func(ro ...llb.RunOption) llb.State {
	dir := "/go/src/" + repo
	return func(ro ...llb.RunOption) llb.State {
		es := s.Dir(dir).Run(ro...)
		es.AddMount(dir, llb.Git(repo, ref, g...))
		return es.AddMount(dir+"/bin", llb.Scratch())
	}
}

func runc(version string) llb.State {
	return goRepo(goBuildBase(), "github.com/opencontainers/runc", version)(
		llb.Shlex("go build -o ./bin/runc ./"),
	)
}

func containerd(version string) llb.State {
	return goRepo(
		goBuildBase().
			Run(llb.Shlex("apk add --no-cache btrfs-progs-dev")).Root(),
		"github.com/containerd/containerd", version, llb.KeepGitDir())(
		llb.Shlex("make bin/containerd"),
	)
}

func buildkit(opt buildOpt) llb.State {
	run := goRepo(goBuildBase(), "github.com/moby/buildkit", "master")

	buildkitd := run(llb.Shlex("go build -o ./bin/buildkitd ./cmd/buildkitd"))

	buildctl := run(llb.Shlex("go build -o ./bin/buildctl ./cmd/buildctl"))

	r := llb.Image("docker.io/library/alpine:latest").With(
		copyAll(buildctl, "/bin"),
		copyAll(buildkitd, "/bin"),
		copyAll(runc(opt.runc), "/bin"),
	)

	if opt.withContainerd {
		r = r.With(
			copyAll(containerd(opt.containerd), "/bin"),
		)
	}
	return r
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
	cpImage := llb.Image("docker.io/library/alpine:latest")
	cp := cpImage.Run(llb.Shlexf("cp -a /src%s /dest%s", srcPath, destPath))
	cp.AddMount("/src", src)
	return cp.AddMount("/dest", dest)
}
