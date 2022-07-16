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
	setStr?:            string // set-string flag values

	// first class flags
	install: *false | true
	atomic:  *false | true
	wait:    *false | true
	dryrun:  *false | true

	// extra args
	flags: [...string]

	// used to avoid name clashes
	let releaseName = name

	_base: #Image
	run:   docker.#Run & {
		input: _base.output
		entrypoint: ["helm"]
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
					if atomic {"--atomic"},
					if wait {"--wait"},
					for path in values {"--values=\(path)"},
					if set != _|_ {"--set=\(strings.Join(strings.Split(set, "\n"), ","))"},
					if setStr != _|_ {"--set-string=\(strings.Join(strings.Split(setStr, "\n"), ","))"},
					if dryrun {"--dry-run"},
				],
				flags,
			])
		}
	}
}
