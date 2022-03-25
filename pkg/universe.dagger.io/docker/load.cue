package docker

import (
	"dagger.io/dagger"
)

// Load an image into a docker daemon
#Load: {
	// Image to load
	image: #Image

	// Name and optionally a tag in the 'name:tag' format
	tag: #Ref

	// Exported image ID
	imageID: _export.imageID

	// Root filesystem with exported file
	result: _export.output

	_export: dagger.#Export & {
		"tag":  tag
		input:  image.rootfs
		config: image.config
	}

	#_cli & {
		mounts: src: {
			dest:     "/src"
			contents: _export.output
		}
		command: {
			name: "load"
			flags: "-i": "/src/image.tar"
		}
	}
}

// FIXME: Move this into docker/client or
// create a better abstraction to reuse here.
#_cli: {
	#_socketConn | #_sshConn | #_tcpConn

	_image: #Pull & {
		source: "docker:20.10.13-alpine3.15"
	}

	input: _image.output
}

// Connect via local docker socket
#_socketConn: {
	host: dagger.#Service

	#Run & {
		mounts: docker: {
			dest:     "/var/run/docker.sock"
			contents: host
		}
	}
}

// Connect via HTTP/HTTPS
#_tcpConn: {
	host: =~"^tcp://.+"

	#Run & {
		env: DOCKER_HOST: host

		// Directory with certificates to verify ({ca,cert,key}.pem files).
		// This enables HTTPS.
		certs?: dagger.#FS

		if certs != _|_ {
			mounts: "certs": {
				dest:     "/certs/client"
				contents: certs
			}
		}
	}
}

// Connect via SSH
#_sshConn: {
	host: =~"^ssh://.+"

	ssh: {
		// Private SSH key
		key?: dagger.#Secret

		// Known hosts file contents
		knownHosts?: dagger.#Secret
	}

	#Run & {
		env: DOCKER_HOST: host

		if ssh.key != _|_ {
			mounts: ssh_key: {
				dest:     "/root/.ssh/id_rsa"
				contents: ssh.key
			}
		}

		if ssh.knownHosts != _|_ {
			mounts: ssh_hosts: {
				dest:     "/root/.ssh/known_hosts"
				contents: ssh.knownHosts
			}
		}
	}
}
