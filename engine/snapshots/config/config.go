package config

import "github.com/dagger/dagger/internal/buildkit/util/compression"

type RefConfig struct {
	Compression            compression.Config
	PreferNonDistributable bool
}
