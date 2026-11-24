package dagger

// TODO: generate with dagger-go-sdk-codegen (github.com/dagger/go-sdk), run by
// //go:generate:container against a dev engine. Then remove generate-library
// and go-client-dev's generate function. Blocked on #14178.
//
// The API schema comes from the introspect tool, which computes it in-process,
// so generating needs no engine.
//go:generate sh -c "go -C ../../ run ./cmd/introspect introspect | go -C ../../ run ./cmd/codegen generate-library --introspection-json-path /dev/stdin --output ./sdk/go"
