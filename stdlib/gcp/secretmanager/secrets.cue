// Google Cloud Secret Manager
package secretmanager

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/gcp"
	"alpha.dagger.io/os"
)

#Secrets: {
	// GCP Config
	config: gcp.#Config

	// Map of secrets
	secrets: [name=string]: dagger.#Secret

	// Deploy encrypted secrets
	deployment: os.#Container & {
		image: gcp.#GCloud & {"config": config}
		shell: path: "/bin/bash"
		always: true

		for name, s in secrets {
			secret: "/tmp/secrets/\(name)": s
		}

		command: #"""
			    # Loop on all files, including hidden files
			    shopt -s dotglob
			    echo "{}" > /tmp/output.json
			    for FILE in /tmp/secrets/*; do
			        BOOL=0 # Boolean
			        gcloud secrets describe "${FILE##*/}" 2>/dev/null > /dev/null
			        status=$?

			        # If secret not found
			        if [[ ! "${status}" -eq 0 ]]; then
			            (\
			                RES=$(gcloud secrets create "${FILE##*/}" --replication-policy automatic --data-file "${FILE}" --format='value(name)' | sed -n '1!p') \
			                && cat <<< $(cat /tmp/output.json | jq ".|.\"${FILE##*/}\"=\"$RES\"") > /tmp/output.json \
			            ) || (echo "Error while creating secret ${FILE##*/}" >&2 && exit 1)
			            BOOL=1
			        else
									(\
											RES=$(gcloud secrets versions add "${FILE##*/}" --data-file "${FILE}" --format='value(name)' | sed -n '1!p') \
											&& cat <<< $(cat /tmp/output.json | jq ".|.\"${FILE##*/}\"=\"$RES\"") > /tmp/output.json \
									) || (echo "Error while updating secret ${FILE##*/}" >&2 && exit 1)
									BOOL=1
			        fi
			        if [ $BOOL -eq 0 ]; then
			            (\
			                RES=$(gcloud secrets describe "${FILE##*/}" --format='value(name)') \
			                && cat <<< $(cat /tmp/output.json | jq ".|.\"${FILE##*/}\"=\"$RES\"") > /tmp/output.json \
			            ) || (echo "Error while retrieving secret ${FILE##*/}" >&2 && exit 1)
			        fi
			    done
			"""#
	}

	// dynamic references
	references: {
		[string]: string
	}

	references: #up: [
		op.#Load & {
			from: deployment
		},

		op.#Export & {
			source: "/tmp/output.json"
			format: "json"
		},
	]
}
