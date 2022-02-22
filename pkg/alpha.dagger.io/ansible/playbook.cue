// Base package to run Ansible operations
package ansible

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"strings"
)

#SSHKeys: {
	// Folder containing ssh keys to mount [required]
	keysSource: dagger.#Artifact @dagger(input)

	// Folder path where to mount keys in the running container (ie: the one referenced in the Playbook configuration) [default="/root/.ssh"]
	destPath: *"/root/.ssh" | string @dagger(input)
}

#Inventory: {
	// Inventory file content [string]
	file: string @dagger(input)

	// Path where the inventory should be located according to config file [default="/etc/hansible/hosts"]
	path: *"/etc/ansible/hosts" | string @dagger(input)
}

#PlaybookConfig: {
	// Config module which sets up ansible [required]
	ansibleConfig: #AnsibleConfig

	// Inventory specifications [optional]
	inventory?: #Inventory

	// Folder containing the ansible project sources (with tasks, vars, etc.) [required]
	source: dagger.#Artifact @dagger(input)

	// Main playbook relative path to run in the project directory [default="main.yml"]
	playbookFile: *"main.yml" | string @dagger(input)

	// SSH keys to use for the playbook [optional]
	sshKeys?: #SSHKeys @dagger(input)
}

#Playbook: {
	// Config for running the playbook [required]
	config: #PlaybookConfig

	// Output configuration of the playbook [required]
	outputPath: *"/logs.txt" | string @dagger(input)

	// List of strings in the form "key1=value1" [list[string], optional]
	extraVars: [string] @dagger(input)

	_ansible: #Ansible & {
		setup: config.ansibleConfig
	}

	// Playbook output
	output: {
		string

		#up: [
			op.#Load & {
				from: _ansible
			},

			if config.inventory != _|_ {
				op.#Exec & {

					args: ["/bin/bash", "--noprofile", "--norc", "-eo", "pipefail", "-c",
						#"""
								INVENTORY_PATH=$(sed 's:^[^/]*::;s/ .*//' <<< $(grep -P -o 'inventory\s+=\s+(.*)' /etc/ansible/ansible.cfg))
								mkdir -p $(dirname $INVENTORY_PATH)
								echo $INVENTORY > $INVENTORY_PATH
							"""#]
					env: {
						ANSIBLE_VERSION: _ansible.setup.version
						INVENTORY:       config.inventory.file
					}
				}
			},

			op.#Exec & {

				mount: {
					"/home/ansible": from: config.source

					if config.sshKeys != _|_ {
						"\(config.sshKeys.destPath)": from: config.sshKeys.keysSource
					}
				}

				args: ["/bin/bash", "--noprofile", "--norc", "-eo", "pipefail", "-c",
					if extraVars == null {
						#"""
                            ansible-playbook "/home/ansible/$MAIN_PLAYBOOK" > /logs.txt
                        """#
					},
					if extraVars != null {
						#"""
                            ansible-playbook "/home/ansible/$MAIN_PLAYBOOK" --extra-vars $EXTRA_VARS > /logs.txt
                        """#
					},
				]
				env: {
					MAIN_PLAYBOOK: config.playbookFile
					if len(extraVars) > 0 {
						EXTRA_VARS: strings.Join(extraVars, " ")
					}
				}
			},

			op.#Export & {
				source: outputPath
				format: string
			},
		]
	} @dagger(output)
}
