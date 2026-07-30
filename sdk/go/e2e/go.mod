module github.com/dagger/dagger/sdk/go/e2e

go 1.26.1

// These tests run both with and without a Dagger session already established.
// With: orchestrated from a Dagger module in CI, or under `dagger api exec`.
// Without: `go test ./...` run straight from a shell.
//
// With one, the client attaches to that session and is served whatever API
// schema it already chose; it cannot ask for a different one
// (dagger/dagger#13701). A released dagger.io/dagger pinned here would break
// against this repo's in-development schema, so we build against the client in
// this repo, which matches by construction.
//
// Without one, the client starts its own session, which means downloading the
// CLI version named in sdk/go/engineconn/version.gen.go. That version is
// unpublished between a VERSION bump and its release; export
// _EXPERIMENTAL_DAGGER_CLI_BIN to run in that window.
require dagger.io/dagger v0.0.0

replace dagger.io/dagger => ..

require (
	github.com/99designs/gqlgen v0.17.89 // indirect
	github.com/Khan/genqlient v0.8.1 // indirect
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dagger/querybuilder v0.0.0-20260402040506-574a5e81cb59 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/sosodev/duration v1.4.0 // indirect
	github.com/vektah/gqlparser/v2 v2.5.32 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.41.0 // indirect
	go.opentelemetry.io/otel/metric v1.41.0 // indirect
	go.opentelemetry.io/otel/trace v1.41.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
)
