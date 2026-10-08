package core

// The core bindings must match the API of this checkout, so they are
// generated against a dev engine built from it (see generateEnv in
// .dagger/modules/go-client-dev).
// Unset the outer session at the process boundary, for the same reason as
// withoutOuterSession in .dagger/modules/engine-dev/main.go. See the generator
// container notes in internal-docs/dagger-codegen.md.
//
//go:generate:container dag://go-client/generate-env
//go:generate env -u DAGGER_SESSION_PORT -u DAGGER_SESSION_TOKEN go -C ../../.. tool dagger-go-sdk-codegen core --output sdk/go/core --dag-output sdk/go/dag
