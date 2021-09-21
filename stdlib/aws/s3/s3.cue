// AWS Simple Storage Service
package s3

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/aws"
)

// S3 Bucket object(s) sync
#Object: {

	// AWS Config
	config: aws.#Config

	// Source Artifact to upload to S3
	source: dagger.#Artifact & dagger.#Input

	// Target S3 URL (eg. s3://<bucket-name>/<path>/<sub-path>)
	target: string & dagger.#Input

	// Delete files that already exist on remote destination
	delete: *false | true & dagger.#Input

	// Object content type
	contentType: *"" | string & dagger.#Input

	// Always write the object to S3
	always: *true | false & dagger.#Input

	// Upload method
	uploadMethod: *"cp" | "sync"

	// URL of the uploaded S3 object
	url: {
		string

		#up: [
			op.#Load & {
				from: aws.#CLI & {
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
					UPLOAD_METHOD: uploadMethod
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
						opts=()
						case "$UPLOAD_METHOD" in
							sync)
								[ -n "$OPT_DELETE" ] && opts+="--delete"
								opts+="--exact-timestamps"
								;;
							cp)
								opts+="--recursive"
								;;
							*)
								echo "not supported command"
								exit 1
								;;
						esac
						[ -n "$OPT_CONTENT_TYPE" ] && opts+="--content-type $OPT_CONTENT_TYPE"
						[ -n "$OPT_DELETE" ] && opts+="--delete"
						aws s3 "$UPLOAD_METHOD" ${opts[@]} /source "$TARGET"
						echo -n "$TARGET" \
							| sed -E 's=^s3://([^/]*)/=https://\1.s3.amazonaws.com/=' \
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
