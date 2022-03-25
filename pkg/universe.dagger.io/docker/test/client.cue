package docker

#Client: {
	#RunSocket | #RunSSH | #RunTCP

	_image: #Pull & {
		source: "docker:20.10.13-alpine3.15"
	}

	input: _image.output
}

// Connect via local docker socket
#RunSocket: {
	host: dagger.#Service

	#Run & {
		mounts: docker: {
			dest:     "/var/run/docker.sock"
			contents: host
		}
	}
}

// Connect via SSH
#RunSSH: {
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

// Connect via HTTP/HTTPS
#RunTCP: {
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
