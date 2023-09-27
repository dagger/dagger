package main

// SDKRuntimes is a map of SDKs to their registry image refs.
//
// When initializing syncing module config, dagger.json will be updated to
// include the specific runtime image.
//
// The refs contained here MUST be compatible with the current Dagger API.
func SDKRuntimes() map[string]string {
	// TODO: until we have real images published, this is hand-maintained.
	return map[string]string{
		"go": "vito/dagger-sdk-go:real-bootstrap",
	}
}
