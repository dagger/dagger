package engine

// A ref is an address for a remote container image
//
// Examples:
//   - "index.docker.io/dagger"
//   - "dagger"
//   - "index.docker.io/dagger:latest"
//   - "index.docker.io/dagger:latest@sha256:a89cb097693dd354de598d279c304a1c73ee550fbfff6d9ee515568e0c749cfe"
#Ref: string

// Container image config. See [OCI](https://www.opencontainers.org/).
// Spec left open on purpose to account for additional fields.
// [Image Spec](https://github.com/opencontainers/image-spec/blob/main/specs-go/v1/config.go)
// [Docker Superset](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/dockerfile2llb/image.go)
#ImageConfig: {
	Env?: [...string]
	User?: string
	Cmd?: [...string]
	...
}

// Download a container image from a remote repository
#Pull: {
	_type: "Pull"

	// Repository source ref
	source: #Ref

	// Authentication
	auth: [...{
		target:   string
		username: string
		secret:   string | #Secret
	}]

	// Root filesystem of downloaded image
	output: #FS

	// Image digest
	digest: string

	// Downloaded container image config
	config: #ImageConfig
}
