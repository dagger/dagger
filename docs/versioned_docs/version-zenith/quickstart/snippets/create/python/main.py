import random
import dagger
from dagger import dag, function

@function
async def build_and_publish(build_src: dagger.Directory, build_args: str, out_file: str) -> str:
    # build project and return binary file
    file = (
        dag
        .golang()
        .with_project(build_src)
        .build(build_args)
        .file(out_file)
    )

    # build and publish container with binary file
    return await (
        dag
        .wolfi()
        .base()
        .container()
        .with_file("/usr/local/bin/dagger", file)
        .publish(f"ttl.sh/my-dagger-container-{random.randrange(10 ** 8)}")
    )
