package main

import (
)

runtime_image_ref: string | *"thechangelog/runtime:2021-05-29T10.17.12Z"

dagger.#Plan & {
	inputs: directories: app: {
		path: "."
		exclude: [
			".circleci",
			".dagger",
			".git",
			".github",
			"2021",
			"2022",
			"_build/dev",
			"_build/test",
			"assets/node_modules",
			"cue.mod",
			"dev_docker",
			"docker",
			"import",
			"nginx",
			"priv/db",
			"priv/uploads",
			"script",
			"tmp",
			".all-contributorsrc",
			".autocomplete",
			".credo.exs",
			".dockerignore",
			".formatter.exs",
			".envrc",
			".env",
			".gitattributes",
			".gitignore",
			"README.md",
			"coveralls.json",
			"start_dev_stack.sh",
			".kube",
			"erl_crash.dump",
			"deps",
			"_build",
			"dagger",
			"main.cue",
		]
	}
	inputs: directories: docker: {
		path: "."
		include: [
			"docker/Dockerfile.production",
			".dockerignore",
		]
	}

	actions: {
		runtimeImage: dagger.#Pull & {
			source: runtime_image_ref
		}

		depsCache: dagger.#CacheDir & {
			id: "depsCache"
		}

		depsCacheMount: "depsCache": {
			dest:     *"/app/deps/" | string
			contents: depsCache
		}

		buildCacheTest: dagger.#CacheDir & {
			id: "buildCacheTest"
		}

		buildCacheTestMount: "buildCacheTest": {
			dest:     *"/app/_build/test" | string
			contents: buildCacheTest
		}

		buildCacheProd: dagger.#CacheDir & {
			id: "buildCacheProd"
		}

		buildCacheProdMount: "buildCacheProd": {
			dest:     *"/app/_build/prod" | string
			contents: buildCacheProd
		}

		nodeModulesCache: dagger.#CacheDir & {
			id: "nodeModulesCache"
		}

		nodeModulesCacheMount: "nodeModulesCache": {
			dest:     *"/app/assets/node_modules" | string
			contents: nodeModulesCache
		}

		appImage: dagger.#Copy & {
			input:    runtimeImage.output
			contents: inputs.directories.app.contents
			dest:     "/app"
		}

		deps: dagger.#Exec & {
			input:   appImage.output
			mounts:  depsCacheMount
			workdir: "/app"
			args: ["bash", "-c", " mix deps.get"]
		}

		assetsCompile: dagger.#Exec & {
			input:   depsCompileProd.output
			mounts:  depsCacheMount & nodeModulesCacheMount
			workdir: "/app/assets"
			env: PATH: "/usr/local/lib/nodejs/node-v14.17.0-linux-x64/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
			args: ["bash", "-c", "yarn install --frozen-lockfile && yarn run compile"]
		}

		#depsCompile: dagger.#Exec & {
			input:   deps.output
			mounts:  depsCacheMount
			workdir: "/app"
			args: ["bash", "-c", "mix do deps.compile, compile"]
		}

		depsCompileTest: #depsCompile & {
			env: MIX_ENV: "test"
			mounts: buildCacheTestMount
		}

		depsCompileProd: #depsCompile & {
			env: MIX_ENV: "prod"
			mounts: buildCacheProdMount
		}

		assetsDigest: dagger.#Exec & {
			input:  assetsCompile.output
			mounts: depsCacheMount & buildCacheProdMount & nodeModulesCacheMount
			env: MIX_ENV: "prod"
			workdir: "/app"
			args: ["bash", "-c", "mix phx.digest"]
		}

		imageProdCacheCopy: dagger.#Exec & {
			input:  assetsDigest.output
			mounts: (depsCacheMount & {depsCache: dest:           "/mnt/app/deps/"} )
			mounts: (buildCacheProdMount & {buildCacheProd: dest: "/mnt/app/_build/prod"} )
			args: ["bash", "-c", "cp -Rp /mnt/app/deps/* /app/deps/ && cp -Rp /mnt/app/_build/prod/* /app/_build/prod/"]
		}

		imageProdDockerCopy: dagger.#Copy & {
			input: imageProdCacheCopy.output
			source: root: inputs.directories.docker.contents
			dest: "/"
		}

		imageProd: dagger.#Build & {
			source: imageProdDockerCopy.output
			dockerfile: path: "/docker/Dockerfile.production"
			buildArg: {
				APP_FROM_PATH: "/app"
				GIT_AUTHOR:    "joel"
				GIT_SHA:       "abcdef"
				APP_VERSION:   "main"
				BUILD_URL:     "longtine.io/build"
			}
		}
	}
}
