package s3

import (
	"dagger.io/dagger"
	"dagger.io/aws"
)

// S3 file or Directory upload
#Put: {

	// AWS Config
	config: aws.#Config

	// Source Artifact to upload to S3
	source?: dagger.#Artifact

	// Source inlined as a string to upload to S3
	sourceInline?: string

	// Target S3 URL (eg. s3://<bucket-name>/<path>/<sub-path>)
	target: string

	// Object content type
	contentType: string | *""

	// URL of the uploaded S3 object
	url: out

	// Always write the object to S3
	always?: bool

	out: string
	aws.#Script & {
		if always != _|_ {
			"always": always
		}
		files: {
			if sourceInline != _|_ {
				"/inputs/source": sourceInline
			}
			"/inputs/target": target
			if contentType != "" {
				"/inputs/content_type": contentType
			}
		}

		export: "/url"

		code: #"""
			opts=""
			op=cp
			if [ -d /inputs/source ]; then
			    op=sync
			fi
			if [ -f /inputs/content_type ]; then
			    opts="--content-type $(cat /inputs/content_type)"
			fi
			aws s3 $op $opts /inputs/source "$(cat /inputs/target)"
			cat /inputs/target \
			    | sed -E 's=^s3://([^/]*)/=https://\1.s3.amazonaws.com/=' \
			    > /url
			"""#

		if sourceInline == _|_ {
			dir: source
		}
	}
}
