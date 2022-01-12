// STARTING POINT: https://docs.dagger.io/1012/ci
// + ../../../.circleci/config.yml
package main

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
	"alpha.dagger.io/os"
)

app_src:            dagger.#Artifact
prod_dockerfile:    dagger.#Input & {string}
docker_host:        dagger.#Input & {string}
dockerhub_username: dagger.#Input & {string}
dockerhub_password: dagger.#Input & {dagger.#Secret}
// ⚠️  Keep this in sync with ../docker/Dockerfile.production
runtime_image_ref: dagger.#Input & {string | *"thechangelog/runtime:2021-05-29T10.17.12Z"}
prod_image_ref:    dagger.#Input & {string | *"thechangelog/changelog.com:dagger"}
build_version:     dagger.#Input & {string}
git_branch:        dagger.#Input & {string | *""}
git_sha:           dagger.#Input & {string}
git_author:        dagger.#Input & {string}
app_version:       dagger.#Input & {string}
build_url:         dagger.#Input & {string}
// ⚠️  Keep this in sync with manifests/changelog/db.yml
test_db_image_ref:      dagger.#Input & {string | *"circleci/postgres:12.6"}
test_db_container_name: "changelog_test_postgres"

// STORY #######################################################################
//
// 1. Migrate from CircleCI to GitHub Actions
//    - extract existing build pipeline into Dagger
//    - run the pipeline locally
//    - run the pipeline in GitHub Actions
//
// 2. Pipeline is 2x quicker (110s vs 228s)
//    - optimistic branching, as pipelines were originally intended
//    - use our own hardware - Linode g6-dedicated-8
//    - predictable runs, no queuing
//    - caching is buildkit layers
//
// 3. Open Telemetry integration out-of-the-box
//    - visualise all steps in Jaeger UI

// PIPELINE OVERVIEW ###########################################################
//
//                  app
//                  |
//                  v
// test_db_start    deps ----------------------------------------\
// |                |                      |                     |
// |                v                      v                     v
// |                deps_compile_test      deps_compile_prod     assets_compile
// |                |                      |               |     |
// |                |                      |               \-----|
// |                v                      v                     |
// \--------------> test                   image_prod_cache      assets_digest
//                  |                      |                     |
// test_db_stop <---|                      v                     |
//                  |                      image_prod <----------/
//                  |                      |
//                  |                      v
//                  \--------------------> image_prod_tag
//
// ========================== BEFORE | AFTER | CHANGE ===================================
// Test, build & push           370s |   10s | 37.00x | https://app.circleci.com/pipelines/github/thechangelog/changelog.com/520/workflows/fbb7c701-d25a-42c1-b42c-db514cd770b4
// + app compile                220s |  100s |  2.20x | https://app.circleci.com/pipelines/github/thechangelog/changelog.com/582/workflows/65500f3d-eccc-49da-9ab0-69846bc812a7
// + deps compile               480s |  110s |  4.36x | https://app.circleci.com/pipelines/github/thechangelog/changelog.com/532/workflows/94f5a339-52a1-45ba-b39b-1bbb69ed6488
//
// Uncached                     ???s |  245s | ?.??x  |
//
// #############################################################################

test_db_start: docker.#Command & {
	host: docker_host
	env: {
		CONTAINER_NAME:  test_db_container_name
		CONTAINER_IMAGE: test_db_image_ref
	}
	command: #"""
		docker container inspect $CONTAINER_NAME \
		  --format 'Container "{{.Name}}" is "{{.State.Status}}"' \
		|| docker container run \
		  --detach --rm --name $CONTAINER_NAME \
		  --publish 127.0.0.1:5432:5432 \
		  --env POSTGRES_USER=postgres \
		  --env POSTGRES_DB=changelog_test \
		  --env POSTGRES_PASSWORD=postgres \
		  $CONTAINER_IMAGE

		docker container inspect $CONTAINER_NAME \
		  --format 'Container "{{.Name}}" is "{{.State.Status}}"'
		"""#
}

app_image: docker.#Pull & {
	from: runtime_image_ref
}

// Put app_src in the correct path, /app
app: os.#Container & {
	image: app_image
	copy: "/app": from: app_src
}

// https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/syntax.md#run---mounttypecache
deps_mount: "--mount=type=cache,id=deps,target=/app/deps/,sharing=shared"

build_test_mount: "--mount=type=cache,id=build_test,target=/app/_build/test,sharing=shared"
build_prod_mount: "--mount=type=cache,id=build_prod,target=/app/_build/prod,sharing=shared"

node_modules_mount: "--mount=type=cache,id=assets_node_modules,target=/app/assets/node_modules,sharing=shared"

deps: docker.#Build & {
	source:     app
	dockerfile: """
		FROM \(runtime_image_ref)
		COPY /app/ /app/
		WORKDIR /app
		RUN \(deps_mount) mix deps.get
		"""
}

assets_compile: docker.#Build & {
	source:     deps
	dockerfile: """
		FROM \(runtime_image_ref)
		COPY /app/ /app/
		WORKDIR /app/assets
		RUN \(deps_mount) \(node_modules_mount) yarn install --frozen-lockfile && yarn run compile
		"""
}

#deps_compile: docker.#Build & {
	source:     deps
	dockerfile: """
		FROM \(runtime_image_ref)
		ARG MIX_ENV
		ENV MIX_ENV=$MIX_ENV
		COPY /app/ /app/
		WORKDIR /app
		RUN \(deps_mount) \(build_test_mount) \(build_prod_mount) mix do deps.compile, compile
		"""
}

deps_compile_test: #deps_compile & {
	args: MIX_ENV: "test"
}

test: docker.#Build & {
	source:     deps_compile_test
	dockerfile: """
		FROM \(runtime_image_ref)
		ENV MIX_ENV=test
		COPY /app/ /app/
		WORKDIR /app
		RUN \(deps_mount) \(build_test_mount) mix test
		"""
}

test_db_stop: docker.#Command & {
	host: docker_host
	env: {
		DEP:            test.dockerfile
		CONTAINER_NAME: test_db_container_name
	}
	command: #"""
		docker container rm --force $CONTAINER_NAME
		"""#
}

deps_compile_prod: #deps_compile & {
	args: MIX_ENV: "prod"
}

assets_digest: docker.#Build & {
	source: assets_compile
	args: ONLY_RUN_AFTER_DEPS_COMPILE_PROD_OK: deps_compile_prod.args.MIX_ENV
	dockerfile: """
		FROM \(runtime_image_ref)
		COPY /app/ /app/
		ENV MIX_ENV=prod
		WORKDIR /app/
		RUN \(deps_mount) \(build_prod_mount) \(node_modules_mount) mix phx.digest
		"""
}

image_prod_cache: docker.#Build & {
	source:     deps_compile_prod
	dockerfile: """
		FROM \(runtime_image_ref)
		COPY /app/ /app/
		WORKDIR /app
		RUN --mount=type=cache,id=deps,target=/mnt/app/deps,sharing=shared cp -Rp /mnt/app/deps/* /app/deps/
		RUN --mount=type=cache,id=build_prod,target=/mnt/app/_build/prod,sharing=shared cp -Rp /mnt/app/_build/prod/* /app/_build/prod/
		"""
}

image_prod: docker.#Command & {
	host: docker_host
	copy: {
		"/tmp/app": from: os.#Dir & {
			from: image_prod_cache
			path: "/app"
		}

		"/tmp/app/priv/static": from: os.#Dir & {
			from: assets_digest
			path: "/app/priv/static"
		}
	}
	env: {
		GIT_AUTHOR:     git_author
		GIT_SHA:        git_sha
		APP_VERSION:    app_version
		BUILD_VERSION:  build_version
		BUILD_URL:      build_url
		PROD_IMAGE_REF: prod_image_ref
	}
	files: "/tmp/Dockerfile":                  prod_dockerfile
	secret: "/run/secrets/dockerhub_password": dockerhub_password
	command: #"""
		cd /tmp
		docker build \
		  --build-arg APP_FROM_PATH=/app \
		  --build-arg GIT_AUTHOR="$GIT_AUTHOR" \
		  --build-arg GIT_SHA="$GIT_SHA" \
		  --build-arg APP_VERSION="$APP_VERSION" \
		  --build-arg BUILD_URL="$BUILD_URL" \
		  --tag "$PROD_IMAGE_REF" .
		"""#
}

if git_branch == "master" {
	image_prod_tag: docker.#Command & {
		host: docker_host
		env: {
			DOCKERHUB_USERNAME:     dockerhub_username
			PROD_IMAGE_REF:         image_prod.env.PROD_IMAGE_REF
			ONLY_RUN_AFTER_TEST_OK: test.dockerfile
		}
		secret: "/run/secrets/dockerhub_password": dockerhub_password
		command: #"""
			docker login --username "$DOCKERHUB_USERNAME" --password "$(cat /run/secrets/dockerhub_password)"
			docker push "$PROD_IMAGE_REF" | tee docker.push.log
			echo "$PROD_IMAGE_REF" > image.ref
			awk '/digest/ { print $3 }' < docker.push.log > image.digest
			"""#
	}
}
