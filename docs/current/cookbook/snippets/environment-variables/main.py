import sys

import anyio
import dagger


async def main():
    # create Dagger client
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        # setup container and
        # define environment variables
        ctr = (
            client
            .container()
            .from_("alpine")
            .with_(env_variables({
                "ENV_VAR_1": "VALUE 1",
                "ENV_VAR_2": "VALUE 2",
                "ENV_VAR_3": "VALUE 3",
            }))
            .with_exec(["env"])
        )

        # print environment variables
        print(await ctr.stdout())


def env_variables(envs: dict[str, str]):
    def env_variables_inner(ctr: dagger.Container):
        for key, value in envs.items():
            ctr = ctr.with_env_variable(key, value)
        return ctr
    return env_variables_inner


anyio.run(main)

