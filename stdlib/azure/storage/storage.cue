// AWS Simple Storage Service
package storage

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/Azure"
)

// S3 Bucket object(s) sync
#Object: {

	// Azure Config
	config: azure.#Config

    // Azure Resource Group
    resourcegroup: string @dagger(input)

    // Azure Account Name
    accountname: string @dagger(input)
	
    // Source Artifact to upload to Azure storage
	source: dagger.#Artifact @dagger(input)

	// Target Azure storage share
	target: string @dagger(input)

	// Delete files that already exist on remote destination
	delete: *false | true @dagger(input)

	// Object content type
	contentType: string | *"" @dagger(input)

	// Always write the object to Azure storage
	always: *true | false @dagger(input)

	// URL of the uploaded Azure storage object
	url: {
		string

		#up: [
			op.#Load & {
				from: azure.#CLI & {
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
					if upload {
						OPT_UPLOAD: "1"
					}
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
                        [ -n "$OPT_DELETE" ] && action+="delete-batch"
                        [ -n "$OPT_UPLOAD" ] && action+="upload" && srcs+="--source /source ${opts[@]}"
                        accountkey=$(az storage account keys list --resource-group resourcegroup --account-name accountname -o tsv --query '[0].value')
                        az storage file ${action[@]}  --account-name accountname --account-key accountkey--share-name "$TARGET" ${srcs[@]} 
                        az storage file url --account-key accountkey --account-name accountname --share-name "$TARGET" --path source  \
							> /url
						"""#,
				]
			},

			op.#Export & {
				source: "/url"
				format: "string"
			},
		]
	} @dagger(output)
}
