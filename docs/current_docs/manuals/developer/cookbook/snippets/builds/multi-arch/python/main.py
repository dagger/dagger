import dagger
from typing import Annotated
from dagger import dag, Doc, function, object_type


@object_type
class MyModule:
    @function
    async def build(
        self, 
        src: Annotated[
            dagger.Directory, 
            Doc("Source code location can be local directory or remote Git repository")]
    ) -> str:
        """Build and publish multi-platform image"""
        # platforms to build for and push in a multi-platform image
        platforms = [
            dagger.Platform("linux/amd64"), # a.k.a. x86_64
            dagger.Platform("linux/arm64"), # a.k.a. aarch64
            dagger.Platform("linux/s390x"), # a.k.a. IBM S/390 
        ]

        # container registry for multi-platform image
        image_repo = "ttl.sh/myapp:latest"

        platform_variants = []
        for platform in platforms:
            # pull golang image for this platform
            ctr = (
                dag.container(platform=platform)
                .from_("golang:1.20-alpine")
                # mount source 
                .with_directory("/src", src)
                # mount empty dir where built binary will live
                .with_directory("/output", dag.directory())
                # ensure binary will be statically linked and thus executable 
                # in the final image 
                .with_env_variable("CGO_ENABLED", "0")
                # build binary and put result at mounted output directory
                .with_workdir("/src")
                .with_exec(["go", "build", "-o", "/output/hello"])
            )

            # select output directory
            output_dir = ctr.directory("/output")

            # wrap output directory in a new empty container marked 
            # with the same platform
            binary_ctr = (
                dag.container(platform=platform)
                .with_rootfs(output_dir)
            )

            platform_variants.append(binary_ctr)
        
        # publish to registry
        image_digest = (
            dag.container()
            .publish(image_repo, platform_variants=platform_variants)
        )

        return await image_digest