import dagger
from dagger import dag, function

def build_base_image() -> dagger.Node:
    return (
        dag.node()
        .with_version("21")
        .with_npm()
        .with_source(dag.current_module().source(".", exclude=[".git", "**/node_modules", "**/sdk"]))
        .install([])
    )
