import { connect, Client } from "@dagger.io/dagger"
import { v4 as uuidv4 } from "uuid";

// initialize Dagger client
connect(async (client: Client) => {

  const platforms = ["linux/amd64", "linux/arm64"]

  const project = client.git("https://github.com/dagger/dagger").branch("main").tree()

  const cache = client.cacheVolume("gomodcache")

  let buildArtifacts = client.directory()

  for (let i = 0; i < platforms.length; i++) {
    const build = client.container({platform: platforms[i]})
    .from("golang:1.21.3-bullseye")
    .withDirectory("/src", project)
    .withWorkdir("/src")
    .withMountedCache("/cache", cache)
    .withEnvVariable("GOMODCACHE", "/cache")
    .withExec(["go", "build", "./cmd/dagger"])

    buildArtifacts = buildArtifacts.withFile(`${platforms[i]}/dagger`, build.file("/src/dagger"))
  }

  await buildArtifacts.export(".")
}, {LogOutput: process.stdout})
