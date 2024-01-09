import { connect, Client } from "@dagger.io/dagger"

// create Dagger client
connect(
  async (client: Client) => {
    // get repository at specified branch
    const project = client
      .git("https://github.com/dagger/dagger")
      .branch("main")
      .tree()

    // return container with repository
    // at /src path
    // excluding *.md files
    const contents = await client
      .container()
      .from("alpine:latest")
      .withDirectory("/src", project, { exclude: ["*.md"] })
      .withWorkdir("/src")
      .withExec(["ls", "/src"])
      .stdout()

    console.log(contents)
  },
  { LogOutput: process.stderr }
)
