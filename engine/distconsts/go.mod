module github.com/dagger/dagger/engine/distconsts

go 1.21.7

// This package is a separate module to avoid weird dependency issues.
//
// Both github.com/dagger/dagger and github.com/dagger/dagger/ci both import
// this so that there's no direct dependency between both of them. This allows
// separate versions in both the top-level and the ci subpackage, which can
// otherwise cause issues.
