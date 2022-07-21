package helm

import (
	"strings"
	"list"

	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

#Upgrade: {
	// the kubeconfig file content
	kubeconfig: dagger.#Secret

	// optionally mount a workspace,
	// useful to read values files from
	workspace?: dagger.#FS

	// base settings
	name:       string // the name of the release
	chart:      string // the chart to use
	repo?:      string // the repo url
	version?:   string // the version to use, if set
	namespace?: string // the namespace to deploy in, if set

	// values
	values: [...string] // list of path to values files
	set?:               string // set flag values
	setString?:         string // set-string flag values

	// first class flags
	cleanupOnFail: *false | true
	debug:         *false | true
	dryRun:        *false | true
	force:         *false | true
	install:       *false | true
	timeout:       string | *"5m"
	wait:          *false | true
	atomic:        *false | true

	username?: string
	password?: dagger.#Secret

	// extra args
	flags: [...string]

	// used to avoid name clashes
	let releaseName = name

	_base: #Image
	run:   docker.#Run & {
		input: _base.output
		entrypoint: ["helm"]
		if workspace != _|_ {
			workdir: "/workspace"
		}
		mounts: {
			"/root/.kube/config": {
				dest:     "/root/.kube/config"
				type:     "secret"
				contents: kubeconfig
			}
			if workspace != _|_ {
				"/workspace": {
					contents: workspace
					dest:     "/workspace"
				}
			}
		}
		env: {
			if password != _|_ {HELM_PASSWORD: password}
		}
		command: {
			name: "upgrade"
			args: list.Concat([
				[
					releaseName,
					chart,
					if repo != _|_ {"--repo=\(repo)"},
					if version != _|_ {"--version=\(version)"},
					if namespace != _|_ {"--namespace=\(namespace)"},
					if install {"--install"},
					if install && namespace != _|_ {"--create-namespace"},
					if atomic {"--atomic"},
					if wait {"--wait"},
					if wait {"--timeout=\(timeout)"},
					for path in values {"--values=\(path)"},
					if set != _|_ {"--set=\(strings.Join(strings.Split(set, "\n"), ","))"},
					if setString != _|_ {"--set-string=\(strings.Join(strings.Split(setString, "\n"), ","))"},
					if debug {"--debug"},
					if dryRun {"--dry-run"},
					if force {"--force"},
					if cleanupOnFail {"--cleanup-on-fail"},
					if username != _|_ {"--username=\(username)"},
					if password != _|_ {"--password=${HELM_PASSWORD}"},
				],
				flags,
			])
		}
	}
}
