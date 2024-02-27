import sys
import anyio
import dagger


async def pipeline():
    async with dagger.Connection(dagger.Config(log_output=sys.stderr)) as client:
        platforms = ["linux/amd64", "linux/arm64"]

        project = client.git("https://github.com/dagger/dagger").branch("main").tree()

        cache = client.cache_volume("gomodcache")

        build_artifacts = client.directory()

        for platform in platforms:
            build = (
            client.container(platform=dagger.Platform(platform))
            .from_("golang:1.21.3-bullseye")
            .with_directory("/src", project)
            .with_workdir("/src")
            .with_mounted_cache("/cache", cache)
            .with_env_variable("GOMODCACHE", "/cache")
            .with_exec(["go", "build", "./cmd/dagger"])
            )
            build_artifacts = build_artifacts.with_file(f'{platform}/dagger', build.file('/src/dagger'))
        await build_artifacts.export('.')
if __name__ == "__main__":
    anyio.run(pipeline)
