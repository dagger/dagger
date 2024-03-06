package util

import (
	"github.com/dagger/dagger/engine/distconsts"

	"dagger/internal/dagger"
)

var dag = dagger.Connect()

const (
	EngineServerPath    = "/usr/local/bin/dagger-engine"
	engineDialStdioPath = "/usr/local/bin/dial-stdio"
	engineShimPath      = distconsts.EngineShimPath

	golangVersion = "1.21.3"
	alpineVersion = "3.18"
	ubuntuVersion = "22.04"
	runcVersion   = "v1.1.12"
	cniVersion    = "v1.3.0"
	qemuBinImage  = "tonistiigi/binfmt@sha256:e06789462ac7e2e096b53bfd9e607412426850227afeb1d0f5dfa48a731e0ba5"

	CacheConfigEnvName = "_EXPERIMENTAL_DAGGER_CACHE_CONFIG"
	GPUSupportEnvName  = "_EXPERIMENTAL_DAGGER_GPU_SUPPORT"
)
