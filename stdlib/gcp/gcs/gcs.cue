// Google Cloud Storage
package gcs

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/gcp"
)

// GCS Bucket object(s) sync
#Object: {

	// GCP Config
	config: gcp.#Config

	// Source Artifact to upload to GCS
	source: dagger.#Artifact & dagger.#Input

	// Target GCS URL (eg. gs://<bucket-name>/<path>/<sub-path>)
	target: string & dagger.#Input

	// Delete files that already exist on remote destination
	delete: *false | true & dagger.#Input

	// Object content type
	contentType: *"" | string & dagger.#Input

	// Always write the object to GCS
	always: *true | false & dagger.#Input

	// URL of the uploaded GCS object
	url: {
		string

		#up: [
			op.#Load & {
				from: gcp.#GCloud & {
					"config": config
				}
			},

			op.#Exec & {
				if always {
					always: true
				}
				env: {
					TARGET:           target
					OPT_CONTENT_TYPE: contentType
					if delete {
						OPT_DELETE: "1"
					}
				}

				mount: "/source": from: source

				args: [
					"/bin/bash",
					"--noprofile",
					"--norc",
					"-eo",
					"pipefail",
					"-c",
					#"""
						opts=("-r")
						[ -n "$OPT_CONTENT_TYPE" ] && opts+="-h Content-Type:$OPT_CONTENT_TYPE"
						[ -n "$OPT_DELETE" ] && opts+="-d"
						gsutil rsync ${opts[@]} /source "$TARGET"
						echo -n "$TARGET" \
							| sed -E 's=^gs://([^/]*)/=https://storage.cloud.google.com/\1/=' \
							> /url
						"""#,

				]
			},

			op.#Export & {
				source: "/url"
				format: "string"
			},
		]
	} & dagger.#Output
}
