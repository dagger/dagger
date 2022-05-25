package function

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
	"universe.dagger.io/x/benjamin.reigner@epitech.eu/gcp/gcr"
)

// The runtimes are copied from https://cloud.google.com/functions/docs/concepts/exec
// If the list would come to change please submit an issue or a pull request to integrate it
#Runtime: "nodejs16" | "nodejs14" | "nodejs12" | "nodejs10" | "nodejs8" | "nodejs6" | "python39" | "python38" | "python37" | "go116" | "go113" | "go111" | "java11" | "dotnet3" | "ruby27" | "ruby26" | "php74"

// The definition used to deploy a serverless cloud function
#Function: {

	// The Config from gcpServerless/configServerless.#Config
	config: gcr.#Credentials
	// The name of the function on gcp, the function developed and the file
	name: string
	// The runtime used for the function
	runtime: #Runtime

	// Directory containing the files for the cloud functions
	source: dagger.#FS

	_functionName: name

	bash.#Run & {
		input:   config.output
		always:  true
		workdir: "/src"
		mounts: "source": {
			dest:     "/src"
			contents: source
		}
		env: {
			FUNCTION_NAME: _functionName
			RUNTIME:       runtime
			REGION:        config.config.region
			PROJECT:       config.config.project
		}
		args: [
			"-c",
			"""
			gcloud functions deploy ${FUNCTION_NAME} --runtime ${RUNTIME} --source /src --trigger-http --allow-unauthenticated --region ${REGION} --project ${PROJECT}
			""",
		]
	}
}
