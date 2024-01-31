import random
import dagger
from dagger import dag, function

@function
def publish() -> str:
    return (
        package()
        .publish(f"ttl.sh/myapp-{random.randrange(10 ** 8)}")
    )

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

def package() -> dagger.Container:
    return (
        dag.container()
        .from_("nginx:1.23-alpine")
        .with_directory("/usr/share/nginx/html", build())
        .with_exposed_port(80)
    )

def build_base_image() -> dagger.Node:
    return (
        dag.node()
        .with_version("21")
        .with_npm()
        .with_source(dag.current_module().source(".", exclude=[".git", "**/node_modules", "**/sdk"]))
        .install([])
    )
