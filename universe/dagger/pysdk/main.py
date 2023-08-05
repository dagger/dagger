from dagger.server import Environment


env = Environment()


@env.check
async def py_lint() -> str:
    """Lint the Python SDK"""
    raise ValueError("Not implemented yet!")

