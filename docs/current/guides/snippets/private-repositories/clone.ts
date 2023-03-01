import Client, { connect } from "@dagger.io/dagger"
import process from "process"

// initialize Dagger client
connect(async (client: Client) => {
  // Collect value of SSH_AUTH_SOCK env var, to retrieve authentication socket path
  const sshAuthSockPath = process.env.SSH_AUTH_SOCK?.toString() || ""

  // Retrieve authentication socket ID from host
  const sshAgentSocketID = await client.host().unixSocket(sshAuthSockPath).id()

  const repo = client
    // Retrieve the repository
    .git("git@private-repository.git")
    // Select the main branch, and the filesystem tree associated
    .branch("main")
    .tree({
      sshAuthSocket: sshAgentSocketID,
    })
    // Select the README.md file
    .file("README.md")

  // Retrieve the content of the README file
  const file = await repo.contents()

  console.log(file)
})
