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
		Run(llb.Shlex("git clone https://github.com/opencontainers/runc.git /go/src/github.com/opencontainers/runc")).
		Dir("/go/src/github.com/opencontainers/runc").
		Run(llb.Shlexf("git checkout -q %s", version)).
		Run(llb.Shlex("go build -o /usr/bin/runc ./")).Root()
}

func containerd(version string) llb.State {
	return goBuildBase().
		Run(llb.Shlex("apk add --no-cache btrfs-progs-dev")).
		Run(llb.Shlex("git clone https://github.com/containerd/containerd.git /go/src/github.com/containerd/containerd")).
		Dir("/go/src/github.com/containerd/containerd").
		Run(llb.Shlexf("git checkout -q %s", version)).
		Run(llb.Shlex("make bin/containerd")).Root()
}

func buildkit(opt buildOpt) llb.State {
	src := goBuildBase().
		Run(llb.Shlex("git clone https://github.com/moby/buildkit.git /go/src/github.com/moby/buildkit")).
		Dir("/go/src/github.com/moby/buildkit")

	buildkitd := src.
		Run(llb.Shlex("go build -o /bin/buildkitd ./cmd/buildkitd"))

	buildctl := src.
		Run(llb.Shlex("go build -o /bin/buildctl ./cmd/buildctl"))

	r := llb.Image("docker.io/library/alpine:latest")
	r = copy(buildctl.Root(), "/bin/buildctl", r, "/bin/")
	r = copy(runc(opt.runc), "/usr/bin/runc", r, "/bin/")
	r = copy(buildkitd.Root(), "/bin/buildkitd", r, "/bin/")
	if opt.withContainerd {
		r = copy(containerd(opt.containerd), "/go/src/github.com/containerd/containerd/bin/containerd", r, "/bin/")
	}
	return r
}

func copy(src llb.State, srcPath string, dest llb.State, destPath string) llb.State {
	cpImage := llb.Image("docker.io/library/alpine:latest")
	cp := cpImage.Run(llb.Shlexf("cp -a /src%s /dest%s", srcPath, destPath))
	cp.AddMount("/src", src)
	return cp.AddMount("/dest", dest)
}
