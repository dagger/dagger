package s3

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/aws"
)

// S3 file or Directory upload
#Put: {

	// AWS Config
	config: aws.#Config

	// Source Artifact to upload to S3
	source?: dagger.#Artifact @dagger(input)

	// Source inlined as a string to upload to S3
	sourceInline?: string @dagger(input)

	// Target S3 URL (eg. s3://<bucket-name>/<path>/<sub-path>)
	target: string @dagger(input)

	// Object content type
	contentType: string | *"" @dagger(input)

	// Always write the object to S3
	always: bool | *true @dagger(input)

	// URL of the uploaded S3 object
	url: {
		@dagger(output)
		string

		#up: [
			op.#Load & {
				from: aws.#CLI & {
					"config": config
				}
			},

			if sourceInline != _|_ {
				op.#WriteFile & {
					dest:    "/source"
					content: sourceInline
				}
			},

			op.#Exec & {
				if always != _|_ {
					"always": always
				}
				env: {
					TARGET:       target
					CONTENT_TYPE: contentType
					ENDPOINT_URL: config.endpointURL
				}

				if sourceInline == _|_ {
					mount: "/source": from: source
				}

				args: [
					"/bin/bash",
					"--noprofile",
					"--norc",
					"-eo",
					"pipefail",
					"-c",
					#"""
						opts=""
						op=cp
						if [ -d /source ]; then
							op=sync
						fi
						if [ -n "$CONTENT_TYPE" ]; then
							opts="--content-type $CONTENT_TYPE"
						fi
						if [ -n "$ENDPOINT_URL" ]; then
							aws s3 --endpoint-url="$ENDPOINT_URL" $op $opts /source "$TARGET"
							echo -n "$TARGET" \
								| sed -E 's=^s3://([^/]*)/='$ENDPOINT_URL'/\1/=' \
								> /url
						else
							aws s3 $op $opts /source "$TARGET"
							echo -n "$TARGET" \
								| sed -E 's=^s3://([^/]*)/=https://\1.s3.amazonaws.com/=' \
								> /url
						fi
						"""#,
				]
			},

			op.#Export & {
				source: "/url"
				format: "string"
			},
		]
	}
}

// S3 Sync
#Sync: {
	// AWS Config
	config: aws.#Config

	// Source Artifact to upload to S3
	source: dagger.#Artifact @dagger(input)

	// Target S3 URL (eg. s3://<bucket-name>/<path>/<sub-path>)
	target: string @dagger(input)

	// Files that exist in the destination but not  in  the
	// source are deleted during sync.
	delete: *false | bool @dagger(input)

	// Object content type
	contentType: string | *"" @dagger(input)

	// Always write the object to S3
	always: bool | *true @dagger(input)

	// URL of the uploaded S3 object
	url: {
		@dagger(output)
		string

		#up: [
			op.#Load & {
				from: aws.#CLI & {
					"config": config
				}
			},

			op.#Exec & {
				if always != _|_ {
					"always": always
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
						opts=()
						if [ -d /source ]; then
							op=sync
						fi
						[ -n "$OPT_CONTENT_TYPE" ] && opts+="--content-type $OPT_CONTENT_TYPE"
						[ -n "$OPT_DELETE" ] && opts+="--delete"
						aws s3 sync ${opts[@]} /source "$TARGET"
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
	}
}
