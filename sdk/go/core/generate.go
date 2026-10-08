package core

// The core bindings must match the API of this checkout, so they are
// generated against a dev engine built from it (see generateEnv in
// .dagger/modules/go-client-dev).
// Nested execs inject the outer engine's session, which takes precedence over
// the configured dev engine; remove it at the generator's process boundary.
//
//go:generate:container dag://go-client/generate-env
//go:generate env -u DAGGER_SESSION_PORT -u DAGGER_SESSION_TOKEN go -C ../../.. tool dagger-go-sdk-codegen core --output sdk/go/core --dag-output sdk/go/dag
