import dagger
from dagger import dag, function

# run unit tests
@function
def test() -> str:
    return (
        build_base_image()
        .run(["run", "test:unit", "run"])
        .stdout()
    )

# build base image
def build_base_image() -> dagger.Node:
    return (
        dag.node()
        .with_version("21")
        .with_npm()
        .with_source(dag.current_module().source())
        .install([])
    )
