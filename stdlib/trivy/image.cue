package trivy

import (
	"encoding/json"

	"alpha.dagger.io/os"
)

// Scan an Image
#Image: {
	// Trivy configuration
	config: #Config

	// Image source (AWS, GCP, Docker Hub, Self hosted)
	source: string

	// Trivy Image arguments
	args: [arg=string]: string
	
	// Enforce args best practices
	args: {
		"--exit-code": *"1" | string
		"--severity": *"HIGH,CRITICAL" | string
		"--format": *"table" | string
		"--ignore-unfixed": *"true" | string
	}

	ctr: os.#Container & {
		image: #CLI & {
			"config": config
		}
		shell: {
			path: "/bin/bash"
			args: ["--noprofile", "--norc", "-eo", "pipefail", "-c"]
		}
		command: #"""
			trivyArgs="$(
				echo "$ARGS" |
				jq -c '
					to_entries |
					map(.key + " " + (.value | tostring) + " ") |
					add
			')"

			trivy image "$trivyArgs" "$SOURCE"
			echo "$SOURCE" > /ref
			"""#
		env: ARGS:   json.Marshal(args)
		env: SOURCE: source
	}

	// Export ref to create dependency (wait for the check to finish)
	ref: {
		os.#File & {
			from: ctr
			path: "/ref"
		}
	}.contents @dagger(output)
}
