import Client, { connect, Container } from "@dagger.io/dagger"
import * as fs from "fs"
import * as path from "path"

connect(async (client: Client) => {
  const gpgFiles = [
    "/home/USER/.gnupg/pubring.kbx",
    "/home/USER/.gnupg/trustdb.gpg",
    "/home/USER/.gnupg/public.key",
  ]

  const sourceDir = "/home/USER/.gnupg/private-keys-v1.d/"
  const targetDir = "/root/.gnupg/private-keys-v1.d/"

  let container = client.container().from("alpine:3.17")

  // Mount GPG files
  for (const filePath of gpgFiles) {
    const secret = client
      .host()
      .setSecretFile(filePath.split("/").pop(), filePath)

    container = container.withMountedSecret(
      `/root/.gnupg/${filePath.split("/").pop()}`,
      secret
    )
  }

  // Mount private keys directory
  container = container.with(
    await mountDirectoryAsSecret(client, sourceDir, targetDir)
  )

  // Sign the binary
  const binaryName = "binary-name"
  container = container
    .withExec(["apk", "add", "--no-cache", "gnupg"])
    .withExec(["chmod", "700", "/root/.gnupg"])
    .withExec(["chmod", "700", "/root/.gnupg/private-keys-v1.d"])
    .withExec(["gpg", "--import", "/root/.gnupg/public.key"])
    .withExec(["gpg", "--import", "/root/.gnupg/private-keys-v1.d/private.key"])
    .withWorkdir("/root")
    .withExec(["gpg", "--detach-sign", "--armor", binaryName])
    .withExec(["ls", "-l", `${binaryName}.asc`])

  console.log(await container.stdout())
})

async function mountDirectoryAsSecret(
  client: Client,
  sourceDir: string,
  targetDir: string
): Promise<(c: Container) => Container> {
  async function processDirectory(
    dir: string
  ): Promise<(c: Container) => Container> {
    const entries = await fs.promises.readdir(dir)
    const containerUpdates: Array<(c: Container) => Container> = []

    for (const entry of entries) {
      const fullPath = path.join(dir, entry)
      const entryStat = await fs.promises.stat(fullPath)

      if (entryStat.isFile()) {
        const secret = client
          .host()
          .setSecretFile(path.basename(fullPath), fullPath)
        const targetPath = path.join(
          targetDir,
          path.relative(sourceDir, fullPath)
        )
        containerUpdates.push((c) => c.withMountedSecret(targetPath, secret))
      } else if (entryStat.isDirectory()) {
        containerUpdates.push(await processDirectory(fullPath))
      }
    }

    return (c) => containerUpdates.reduce((acc, update) => update(acc), c)
  }

  return processDirectory(sourceDir)
}
