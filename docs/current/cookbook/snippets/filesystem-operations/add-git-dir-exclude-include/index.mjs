import { connect } from "@dagger.io/dagger"

// create Dagger client
connect(
  async (client) => {
    // get repository at specified branch
    const project = client
      .git("https://github.com/dagger/dagger")
      .branch("main")
      .tree()

    // return container with repository
    // at /src path
  	// including all files except files beginning with .git
    const contents = await client
      .container()
      .from("alpine:latest")
      .withDirectory("/src", project, { include: ["*"], exclude: [".git*"] })
      .withWorkdir("/src")
      .withExec(["ls", "-a", "/src"])
      .stdout()

    console.log(contents)
  },
  { LogOutput: process.stderr }
)
