package core

// The core bindings must match the API of this checkout, so they are
// generated against a dev engine built from it (see generateEnv in
// .dagger/modules/go-client-dev).
//
//go:generate:container go-client:generate-env
//go:generate go -C ../../.. tool dagger-go-sdk-codegen core --output sdk/go/core --dag-output sdk/go/dag
