import sys
import anyio
import dagger

async def main():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:

        secret_env = client.set_secret("my-secret-var", "secret value here")
        secret_file = client.set_secret("my-secret-file", "secret file content here")

        # dump secrets to console
        out = await (
          client.container()
          .from_("alpine:3.17")
          .with_secret_variable("MY_SECRET_VAR", secret_env)
          .with_mounted_secret("/my_secret_file", secret_file)
          .with_exec(["sh", "-c", """ echo -e "secret env data: $MY_SECRET_VAR || secret file data: "; cat /my_secret_file """])
          .stdout()
        )

    print(out)

anyio.run(main)