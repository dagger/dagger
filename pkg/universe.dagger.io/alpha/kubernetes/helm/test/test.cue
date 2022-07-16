package helm

import (
	"dagger.io/dagger"
	"universe.dagger.io/alpha/kubernetes/helm"
)

dagger.#Plan & {
	client: {
		filesystem: "./testdata": read: contents: dagger.#FS
		env: KUBECONFIG: string
		commands: kubeconfig: {
			name: "cat"
			args: ["\(env.KUBECONFIG)"]
			stdout: dagger.#Secret
		}
	}
	actions: test: {
		install: {
			URL: helm.#Install & {
				name:       "test-pgsql"
				source:     "URL"
				URL:        "https://charts.bitnami.com/bitnami/postgresql-11.1.12.tgz"
				kubeconfig: client.commands.kubeconfig.stdout
			}
			repository: helm.#Install & {
				name:       "test-redis"
				source:     "repository"
				chart:      "redis"
				repoName:   "bitnami"
				repository: "https://charts.bitnami.com/bitnami"
				kubeconfig: client.commands.kubeconfig.stdout
			}
		}
		upgrade: {
			repo: helm.#Upgrade & {
				kubeconfig: client.commands.kubeconfig.stdout
				workspace:  client.filesystem.".".read.contents
				name:       "test-redis-repo"
				repo:       "https://charts.bitnami.com/bitnami"
				chart:      "redis"
				version:    "17.0.1"
				namespace:  "sandbox"
				install:    true
				atomic:     true
				dryrun:     true
				flags: ["--skip-crds"]
				values: ["values.base.yaml", "values.staging.yaml"]
				set: #"""
					auth.enabled=false
					master.disableCommands={}
					"""#
			}
			chart: helm.#Upgrade & {
				kubeconfig: client.commands.kubeconfig.stdout
				name:       "test-redis-chart"
				chart:      "https://charts.bitnami.com/bitnami/redis-17.0.1.tgz"
				dryrun:     true
				install:    true
				setStr: #"""
					master.podAnnotations.n=1
					master.podLabels.n=2
					"""#
			}
		}
	}
}
