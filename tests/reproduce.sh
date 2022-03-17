#!/bin/bash

set -exo pipefail

echo "Our team shares this remote Docker Engine running on Linux"
export DOCKER_HOST=ssh://root@137.184.58.110
docker info

echo "Make sure that you install docker via Docker Desktop otherwise it will not have the buildx plugin which is required for this to work."
docker buildx version

echo "Start dagger-buildkitd via buildx..."
docker buildx inspect dagger-buildkitd \
|| docker buildx create --name dagger-buildkitd --driver docker-container --use --bootstrap

# Export env
export BUILDKIT_HOST=docker-container://buildx_buildkit_dagger-buildkitd0

# Test
export DAGGER_LOG_FORMAT=plain
export DAGGER_LOG_LEVEL=debug
cd cache
dagger do build --cache-to type=local,dest=toto,mode=max --cache-from type=local,src=toto

find toto

echo "Down dagger-buildkitd via buildx..."
docker buildx rm dagger-buildkitd

echo "Restarting dagger-buildkitd via buildx..."
docker buildx create --name dagger-buildkitd --driver docker-container --use --bootstrap

echo "Re execute dagger do : instruction should be cached and take less than 10 seconds"
dagger do build --cache-to type=local,dest=toto,mode=max --cache-from type=local,src=toto

echo "Down dagger-buildkitd via buildx to clean up execution..."
docker buildx rm dagger-buildkitd