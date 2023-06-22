import Client, { Container, serveCommands } from "@dagger.io/dagger"

serveCommands(build, test)

/**
 * Build the go binary from the given repo, branch and subpath.
 * @param client Dagger client (ignored by the CLI).
 * @param repo the git repository to clone.
 * @param branch the specific branch to clone.
 * @param subpath the subpath to clone.
 * @returns the error output of the build.
 */
async function build(
  client: Client,
  repo: string,
  branch: string,
  subpath: string
): Promise<string> {
  console.log("repository: '", repo, "'")
  console.log("branch: '", branch, "'")
  return await base(client, repo, branch)
    .withExec([
      "go",
      "build",
      "-v",
      "-x",
      "-o",
      "/usr/local/binary",
      `./${subpath}`,
    ])
    .stderr()
}

/**
 * Test the go binary from the given repo, branch and subpath.
 * @param client Dagger client (ignored by the CLI).
 * @param repo the git repository to clone.
 * @param branch the specific branch to clone.
 * @param subpath the subpath to clone.
 * @returns the standard output of the executed command.
 */
async function test(
  client: Client,
  repo: string,
  branch: string,
  subpath: string
): Promise<string> {
  return await base(client, repo, branch)
    .withExec(["go", "test", "-v", `./${subpath}`])
    .stdout()
}

function base(client: Client, repo: string, branch: string): Container {
  if (branch === "") {
    branch = "main"
  }

  return client
    .container()
    .from("golang:1.20-alpine")
    .withMountedCache("/go/pkg/mod", client.cacheVolume("go-mod"))
    .withMountedCache("/root/.cache/go-build", client.cacheVolume("go-build"))
    .withMountedDirectory("/src", client.git(repo).branch(branch).tree())
    .withWorkdir("/src")
}
