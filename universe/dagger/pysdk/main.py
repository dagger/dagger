import dagger
from dagger.server import Environment


env = Environment()


@env.check
async def py_lint() -> str:
    """Lint the Python SDK"""
    return await (
        dagger.container()
        .from_("python:3.11.1-alpine")
        .with_exec(["python", "-V"])
        .stdout()
    )

