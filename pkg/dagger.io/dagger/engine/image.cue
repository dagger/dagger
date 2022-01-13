package engine

// Upload a container image to a remote repository
#Push: {
	$dagger: task: _name: "Push"

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
	$dagger: task: _name: "Pull"

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

// Build a container image using a Dockerfile
#Dockerfile: {
	$dagger: task: _name: "Dockerfile"

	// Source directory to build
	source: #FS

	dockerfile: *{
		path: string | *"Dockerfile"
	} | {
		contents: string
	}

	// Authentication
	auth: [...{
		target:   string
		username: string
		secret:   string | #Secret
	}]

	platforms?: [...string]
	target?: string
	buildArg?: [string]: string
	label?: [string]:    string
	hosts?: [string]:    string

	// Root filesystem produced
	output: #FS

	// Container image config produced
	config: #ImageConfig
}
