package shim

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sync"

	"github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

//go:embed cmd/*
var cmd embed.FS

var (
	state llb.State
	lock  sync.Mutex
)

const Path = "/_shim"
const WasmPath = "/_shim.wasm"

func init() {
	entries, err := fs.ReadDir(cmd, "cmd")
	if err != nil {
		panic(err)
	}

	state = llb.Scratch()
	for _, e := range entries {
		contents, err := fs.ReadFile(cmd, path.Join("cmd", e.Name()))
		if err != nil {
			panic(err)
		}

		state = state.File(llb.Mkfile(e.Name(), e.Type().Perm(), contents))
		e.Name()
	}
}

// TODO: includeWasm is clunky
func Build(ctx context.Context, gw bkgw.Client, p specs.Platform, includeWasm bool) (llb.State, error) {
	lock.Lock()
	def, err := state.Marshal(ctx, llb.Platform(p))
	lock.Unlock()
	if err != nil {
		return llb.State{}, err
	}

	opts := map[string]string{
		"platform": platforms.Format(p),
	}
	inputs := map[string]*pb.Definition{
		dockerfilebuilder.DefaultLocalNameContext:    def.ToPB(),
		dockerfilebuilder.DefaultLocalNameDockerfile: def.ToPB(),
	}
	res, err := gw.Solve(ctx, bkgw.SolveRequest{
		Frontend:       "dockerfile.v0",
		FrontendOpt:    opts,
		FrontendInputs: inputs,
	})
	if err != nil {
		return llb.State{}, err
	}

	bkref, err := res.SingleRef()
	if err != nil {
		return llb.State{}, err
	}

	st, err := bkref.ToState()
	if err != nil {
		return llb.State{}, err
	}

	if includeWasm {
		wasmSt, err := wasmShim(gw, p)
		if err != nil {
			return llb.State{}, err
		}
		st = llb.Merge([]llb.State{st, wasmSt})
	}
	return st, nil
}

func wasmShim(gw bkgw.Client, p specs.Platform) (llb.State, error) {
	// TODO: more robust conversion matrix
	var arch string
	switch p.Architecture {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	default:
		return llb.State{}, fmt.Errorf("FIXME: compile wasmtime for this architecture %s", p.Architecture)
	}

	repo := llb.Git("https://github.com/bytecodealliance/wasmtime.git", "v2.0.1")

	st := llb.Image("rust:1-alpine", llb.WithMetaResolver(gw)).Run(
		llb.Shlex("apk add --no-cache musl-dev build-base"),
	).Run(
		llb.Shlex("rustup target add x86_64-unknown-linux-musl"),
	).Run(
		llb.Dir("/mnt"),
		llb.AddMount("/usr/local/cargo/registry", llb.Scratch(), llb.AsPersistentCacheDir("cargoregistryshimcache", llb.CacheMountShared)),
		llb.Shlex(fmt.Sprintf(
			"cargo build --release --target %s-unknown-linux-musl",
			arch,
		)),
	).AddMount("/mnt", repo)

	st = llb.Scratch().File(llb.Copy(st, fmt.Sprintf(
		"/target/%s-unknown-linux-musl/release/wasmtime",
		arch,
	), WasmPath))
	return st, nil
}

/*
// it's possible to embed wasmedge in go, but their instructions currently
// require you have an .so around that's loaded by go, which is annoying, so
// instead just building their CLI and having Exec handle calling that as
// the shim.
// https://wasmedge.org/book/en/sdk/go.html

	repo := llb.Git("https://github.com/WasmEdge/WasmEdge.git", "0.11.1")

	// https://wasmedge.org/book/en/contribute/build_from_src/linux.html
	// https://wasmedge.org/book/en/contribute/build_from_src.html#cmake-building-options
	src := llb.Image("ubuntu:20.04").Run(
		llb.Args([]string{"sh", "-c", strings.Join([]string{
			"apt-get update",
			"DEBIAN_FRONTEND=noninteractive apt-get install -y git build-essential software-properties-common cmake libboost-all-dev llvm-12-dev liblld-12-dev clang-12",
		}, " && ")}),
	).Run(
		llb.Dir("/src"),
		llb.Args([]string{"sh", "-c", strings.Join([]string{
			"apt install zlib1g-dev",
			"mkdir -p build && cd build",
			"cmake -DCMAKE_BUILD_TYPE=Release -DWASMEDGE_LINK_TOOLS_STATIC=ON -DWASMEDGE_LINK_LLVM_STATIC=ON -DWASMEDGE_BUILD_STATIC_LIB=ON -DWASMEDGE_BUILD_SHARED_LIB=OFF -DWASMEDGE_FORCE_DISABLE_LTO=ON -DWASMEDGE_BUILD_PLUGINS=OFF .. && make -j",
		}, " && ")}),
	).AddMount("/src", repo)

	st := llb.Image("ubuntu:20.04").Run(
		llb.Shlex("ldd /src/build/tools/wasmedge/wasmedge"),
		// llb.Shlex("tree /src/build"),
		llb.AddMount("/src", src),
	).Root()

	// st := llb.Scratch().File(llb.Copy(src, "/build/tools/wasmedge/wasmedge", Path))
	return st, nil
*/
