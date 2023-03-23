import sys

import anyio

import dagger


async def test():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        database = (
            client.container()
            .from_("postgres:15.2")
            .with_env_variable("POSTGRES_PASSWORD", "test")
            .with_exec(["postgres"])
            .with_exposed_port(5432)
        )

        src = client.host().directory(".")

        pytest = (
            client.container()
            .from_("python:3.10-slim-buster")
            .with_service_binding("db", database) # bind database with the name db
            .with_env_variable("DB_HOST", "db") # db refers to the service binding
            .with_env_variable("DB_PASSWORD", "test") # password set in db container
            .with_env_variable("DB_USER", "postgres") # default user in postgres image
            .with_env_variable("DB_NAME", "postgres") # default db name in postgres image
            .with_mounted_directory("/src", src)
            .with_workdir("/src")
            .with_exec(["apt-get", "update"])
            .with_exec(["apt-get", "install", "-y", "libpq-dev", "gcc"])
            .with_exec(["pip", "install", "pytest", "psycopg2"])
            .with_exec(["pytest"]) # execute pytest
        )

        # execute
        results = await pytest.stdout()

    print(results)


if __name__ == "__main__":
    anyio.run(test)