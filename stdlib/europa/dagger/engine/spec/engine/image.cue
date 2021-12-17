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

// Download a container image from a remote repository
#Pull: $dagger: task: _name: "Pull"

// Build a container image using buildkit
// FIXME: rename to #Dockerfile to clarify scope
#Build: {
	$dagger: task: _name: "Build"

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
