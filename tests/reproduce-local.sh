#!/bin/bash

set -exo pipefail

echo "Start dagger-buildkitd..."
docker inspect dagger-buildkitd > /dev/null || docker run --net=host -d --restart always -v dagger-buildkitd:/var/lib/buildkit --name dagger-buildkitd --privileged  moby/buildkit

# Export env
export BUILDKIT_HOST=docker-container://dagger-buildkitd

echo "Run go build"
export DAGGER_LOG_FORMAT=plain
export DAGGER_LOG_LEVEL=debug
cd cache
dagger do build --cache-to type=local,dest=storage,mode=max --cache-from type=local,src=storage

find storage

echo "Down dagger-buildkitd..."
docker container stop dagger-buildkitd && docker container rm dagger-buildkitd && docker volume rm dagger-buildkitd

echo "Restarting dagger-buildkitd..."
docker run --net=host -d --restart always -v dagger-buildkitd:/var/lib/buildkit --name dagger-buildkitd --privileged  moby/buildkit

echo "Rerun dagger do (should last less than 10 seconds)"
dagger do build --cache-to type=local,dest=storage,mode=max --cache-from type=local,src=storage

echo "Down dagger-buildkitd to clean up execution..."
docker container stop dagger-buildkitd && docker container rm dagger-buildkitd && docker volume rm dagger-buildkitd
