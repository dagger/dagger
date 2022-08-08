package helm

import (
	"universe.dagger.io/docker"
)

#KubeloginImage: {
	kubeloginVersion: *"0.0.20" | string
	docker.#Build & {
		steps: [
			docker.#Pull & {
				version: string | *"3.9.1"
				// https://hub.docker.com/r/alpine/helm/tags
				source: "index.docker.io/alpine/helm:\(version)"
			},
			docker.#Run & {
				workdir: "/tmp"
				entrypoint: ["/bin/sh", "-c"]
				command: name: #"""
                    set eu
                    apk --no-cache --update add curl unzip
                    curl -fsSL https://github.com/Azure/kubelogin/releases/download/v\#(kubeloginVersion)/kubelogin-linux-amd64.zip >kubelogin.zip
                    unzip -q kubelogin.zip
                    mv bin/linux_amd64/kubelogin /usr/local/bin
                    rm -rf bin kubelogin.zip
                    chmod +x /usr/local/bin/kubelogin
                    rm -rf /tmp/*
                    """#
			},
			docker.#Set & {
				config: env: AAD_LOGIN_METHOD: "spn"
			},
		]
	}
}
