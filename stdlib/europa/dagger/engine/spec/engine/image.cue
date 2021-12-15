package engine

// Container image config
// See [OCI](https://www.opencontainers.org)
#ImageConfig: {
	env?: [...string]
	user?: string
	command?: [...string]
	// FIXME
}

// Upload a container image to a remote repository
#Push: {
	push: {}

	// Target repository address
	dest: #Ref

	// Filesystem contents to push
	input: #FS

	// Container image config
	config: #ImageConfig

	// Authentication
	auth: [...{
		target:   string
		username: string
		secret:   string | #Secret
	}]

	// Complete ref of the pushed image, including digest
	result: #Ref
}

// Download a container image from a remote repository
#Pull: {
	pull: {}

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

	// Complete ref of downloaded image (including digest)
	result: #Ref

	// Downloaded container image config
	config: #ImageConfig
}

// A ref is an address for a remote container image
//
// Examples:
//   - "index.docker.io/dagger"
//   - "dagger"
//   - "index.docker.io/dagger:latest"
//   - "index.docker.io/dagger:latest@sha256:a89cb097693dd354de598d279c304a1c73ee550fbfff6d9ee515568e0c749cfe"
#Ref: string

// Build a container image using buildkit
#Build: {
	build: {}

	// Source directory to build
	source: #FS
	{
		frontend:   "dockerfile"
		dockerfile: {
			path: string | *"Dockerfile"
		} | {
			contents: string
		}
	}

	// Root filesystem produced by build
	output: #FS

	// Container image config produced by build
	config: #ImageConfig
}
