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
		"--severity":       *"HIGH,CRITICAL" | string
		"--exit-code":      *"1" | string
		"--ignore-unfixed": *"" | string
		"--format":         *"table" | string
		"--output":         *"output" | string
	}

	ctr: os.#Container & {
		image: #CLI & {
			"config": config
		}
		shell: {
			path: "/bin/bash"
			args: ["--noprofile", "--norc", "-eo", "pipefail", "-c"]
		}
		always: true
		command: #"""
			trivyArgs="$(
				echo "$ARGS" |
				jq -c '
					to_entries |
					map(.key + " " + (.value | tostring) + " ") |
					add
			')"

			# Remove suffix and prefix quotes if present
			trivyArgs="${trivyArgs#\"}"
			trivyArgs="${trivyArgs%\"}"

			trivy image $trivyArgs "$SOURCE"
			echo -n "$SOURCE" > /ref
			"""#
		env: ARGS:   json.Marshal(args)
		env: SOURCE: source
	}

	// Reference analyzed
	ref: {
		os.#File & {
				from: ctr
				path: "/ref"
			}
	}.contents @dagger(output)
}
