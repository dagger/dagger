package ansible

import (
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
)

#AnsibleConfig: {
	// Version of Ansible to use [default="2.9"]
	version: *"2.9" | string @dagger(input)

	// Ansible config file [optional]
	configFile: *#DefaultAnsibleConfig | string @dagger(input)
}

#Ansible: {
	// Configuration of Ansible [required]
	setup: #AnsibleConfig

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				package: bash:          "=~5.1"
				package: gcc:           true
				package: grep:          true
				package: "openssl-dev": true
				package: "py3-pip":     true
				package: "python3-dev": true
				package: "gpgme-dev":   true
				package: "libc-dev":    true
				package: rust:          true
				package: cargo:         true
			}
		},

		op.#Mkdir & {
			path: "/etc/ansible"
		},

		op.#WriteFile & {
			dest:    "/etc/ansible/ansible.cfg"
			content: "\(setup.configFile)"
		},

		op.#Exec & {
			args: ["/bin/bash", "--noprofile", "--norc", "-eo", "pipefail", "-c",
				#"""
						pip install ansible==$ANSIBLE_VERSION   
					"""#]
			env: ANSIBLE_VERSION: "\(setup.version)"
		},
	]
}
