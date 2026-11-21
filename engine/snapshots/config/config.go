package config

import (
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"golang.org/x/sync/semaphore"
)

type RefConfig struct {
	Compression            compression.Config
	PreferNonDistributable bool
	// Listings exports each layer the export computes as a listing and its
	// content blobs, as the remote cache carries layers, in place of a tar.
	// A layer that already has a blob keeps it.
	Listings bool
	// Builds bounds how an export builds the blobs its layers lack. The zero
	// value builds them one at a time.
	Builds LayerBuilds
}

// LayerBuilds bounds building an export's missing layer blobs: diffing each
// layer against its parent, compressing and writing the diff.
type LayerBuilds struct {
	// Workers is how many of one export's layers build at once; 0 or 1
	// builds them one at a time, in chain order.
	Workers int
	// Gate, when set, bounds the builds of every export that shares it, one
	// unit per build. It is held only while a blob is built, not while a
	// layer's blob is reused or its export lock is awaited.
	Gate *semaphore.Weighted
}
