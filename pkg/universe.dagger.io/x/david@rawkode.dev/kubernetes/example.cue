package rawkode_kubernetes_example

import (
	"dagger.io/dagger"
	"universe.dagger.io/x/david@rawkode.dev/kubernetes:kubectl"
)

dagger.#Plan & {
	client: {
		filesystem: "./": read: contents: dagger.#FS
		commands: kubeconfig: {
			name: "kubectl"
			args: ["config", "view", "--raw"]
			stdout: dagger.#Secret
		}
	}

	actions: rawkode: kubectl.#Apply & {
		manifests: core.#Subdir & {
			input: client.filesystem."./".read.contents
			path:  "/kubernetes"
		}
		kubeconfigSecret: client.commands.kubeconfig.stdout
	}
}
