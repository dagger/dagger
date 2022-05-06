package kubernetes

import (
	"dagger.io/dagger"

	"universe.dagger.io/x/tom.chauveau.pro@icloud.com/kubernetes"
)

dagger.#Plan & {
	// Kubeconfig must be in current directory
	client: {
		filesystem: "./data": read: contents: dagger.#FS
		commands: kubeconfig: {
			name: "kubectl"
			args: ["config", "view", "--raw"]
			stdout: dagger.#Secret
		}
	}

	actions: test: apply: {
		// Alias on kubeconfig
		_kubeconfig: client.commands.kubeconfig.stdout

		url: kubernetes.#Apply & {
			kubeconfig: _kubeconfig
			location:   "url"
			url:        "https://gist.githubusercontent.com/grouville/04402633618f3289a633f652e9e4412c/raw/293fa6197b78ba3fad7200fa74b52c62ec8e6703/hello-world-pod.yaml"
		}

		directory: kubernetes.#Apply & {
			kubeconfig: _kubeconfig
			location:   "directory"
			source:     client.filesystem."./data".read.contents
		}
	}
}
