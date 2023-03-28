import sys
import os
import anyio
import dagger

async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:

        # set a test host environment variable
        os.environ["MY_SECRET_VAR"] = "secret value here"

        # set a test host file
        await anyio.Path("my_secret_file").write_text("secret file content here")

        # load secrets
        secret_env = (
          client
          .host()
          .env_variable("MY_SECRET_VAR")
          .secret()
        )

        secret_file = (
          client
          .host()
          .directory(".")
          .file("my_secret_file")
          .secret()
        )

        # dump secrets to console
        output = await (
          client.container()
          .from_("alpine:3.17")
          .with_secret_variable("MY_SECRET_VAR", secret_env)
          .with_mounted_secret("/my_secret_file", secret_file)
          .with_exec(["sh", "-c", """ echo -e "secret env data: $MY_SECRET_VAR || secret file data: "; cat /my_secret_file """])
          .stdout()
        )

        print(output)

anyio.run(main)
