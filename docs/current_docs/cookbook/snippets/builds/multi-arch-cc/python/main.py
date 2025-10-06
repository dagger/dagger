from typing import Annotated

import dagger
from dagger import Doc, dag, function, object_type


@object_type
class MyModule:
    @function
    async def build(
        self,
        src: Annotated[
            dagger.Directory,
            Doc(
                "Source code location can be local directory or remote Git \
                repository"
            ),
        ],
    ) -> str:
        """Build an publish multi-platform image"""
        # platforms to build for and push in a multi-platform image
        platforms = [
            dagger.Platform("linux/amd64"),  # a.k.a. x86_64
            dagger.Platform("linux/arm64"),  # a.k.a. aarch64
            dagger.Platform("linux/s390x"),  # a.k.a. IBM S/390
        ]

        # container registry for multi-platform image
        image_repo = "ttl.sh/myapp:latest"

        platform_variants = []
        for platform in platforms:
            # parse architecture using containerd utility module
            platform_arch = await dag.containerd().architecture_of(platform)

            # pull golang image for the *host* platform, this is done by
            # not specifying the a platform. The default is the host platform.
            ctr = (
                dag.container()
                .from_("golang:1.21-alpine")
                # mount source
                .with_directory("/src", src)
                # mount empty dir where built binary will live
                .with_directory("/output", dag.directory())
                # ensure binary will be statically linked and thus executable
                # in the final image
                .with_env_variable("CGO_ENABLED", "0")
                # configure go compiler to use cross-compilation targeting the
                # desired platform
                .with_env_variable("GOOS", "linux")
                .with_env_variable("GOARCH", platform_arch)
                # build binary and put result at mounted output directory
                .with_workdir("/src")
                .with_exec(["go", "build", "-o", "/output/hello"])
            )

            # selelct output directory
            output_dir = ctr.directory("/output")

            # wrap output directory in a new empty container marked
            # with the same platform
            binary_ctr = (
                dag.container(platform=platform)
                .with_rootfs(output_dir)
                .with_entrypoint(["/hello"])
            )

            platform_variants.append(binary_ctr)

        # publish to registry
        image_digest = dag.container().publish(
            image_repo, platform_variants=platform_variants
        )

        return await image_digest
