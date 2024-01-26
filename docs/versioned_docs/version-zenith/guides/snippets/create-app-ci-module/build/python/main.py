import dagger
from dagger import dag, function

@function
def build() -> dagger.Directory:
    return (
        build_base_image()
        .build()
        .container()
        .directory("./dist")
    )

@function
def test() -> str:
    return (
        build_base_image()
        .run(["run", "test:unit", "run"])
        .stdout()
    )

def build_base_image() -> dagger.Node:
    return (
        dag.node()
        .with_version("21")
        .with_npm()
        .with_source(dag.host().directory(".", exclude=[".git", "**/node_modules", "**/sdk"]))
        .install([])
    )
