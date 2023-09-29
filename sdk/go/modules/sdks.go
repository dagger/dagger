package modules

// WellKnownSDKRuntimes maps well-known SDK names to their runtime image ref.
//
// The refs contained here must be compatible with the current Dagger API.
//
// The dagger.json module config stores both an SDK name and an image ref. When
// running dagger mod sync, the Dagger CLI will automatically update the
// sdkRuntime field in config.json to reflect the image ref from this map.
//
// TODO: align with engineconn.CLIVersion once we're publishing these in CI.
// For now it's just hardcoded.
//
// TODO: consider dropping this, and just having a convention where the SDK
// name is shorthand for registry.dagger.io/sdk-<name> and the "pinning" just
// behaves like pinning a regular module.
//
// TODO: consider replacing this with a module ref instead and bootstrapping by
// building a Dockerfile. We would still want some sort of shorthand though. No
// one wants to type dagger mod init --sdk=github.com/dagger/dagger-sdk-go.
var WellKnownSDKRuntimes = map[string]string{
	"go": "vito/dagger-sdk-go:no-vcs-flag@sha256:0fcc9d2659cdd551acb7aaf25e77215e9687475eb89ec06cae989eaf332ee480",
}
