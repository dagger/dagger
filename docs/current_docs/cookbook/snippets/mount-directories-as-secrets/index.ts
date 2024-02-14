import { Client, connect, Container } from "@dagger.io/dagger"
import * as fs from "fs"
import * as glob from "glob"
import * as path from "path"

const GPG_KEY = process.env.GPG_KEY || "public"

connect(
  async (client: Client) => {
    await client
      .container()
      .from("alpine:3.17")
      .withExec(["apk", "add", "--no-cache", "gnupg"])
      .with(await mountedSecretDirectory(client, "/root/.gnupg", "~/.gnupg"))
      .withWorkdir("/root")
      .withMountedFile("myapp", client.host().file("myapp"))
      .withExec(["gpg", "--detach-sign", "--armor", "-u", GPG_KEY, "myapp"])
      .file("myapp.asc")
      .export("myapp.asc")
  },
  { LogOutput: process.stderr },
)

async function mountedSecretDirectory(
  client: Client,
  targetPath: string,
  sourcePath: string,
): Promise<(c: Container) => Container> {
  sourcePath = path.resolve(
    process.env.HOME || process.env.USERPROFILE || "",
    sourcePath.substring(2),
  )
  const globFiles = glob.sync(`${sourcePath}/**/*`, { nodir: true })
  const files = globFiles.filter((file) => fs.statSync(file).isFile())

  function _mountedSecretDirectory(container: Container) {
    for (const file of files) {
      const relative = path.relative(sourcePath, file)
      const secret = client.host().setSecretFile(relative, file)
      container = container.withMountedSecret(
        path.join(targetPath, relative),
        secret,
      )
    }

    // Fix directory permissions
    return container.withExec([
      "sh",
      "-c",
      `find ${targetPath} -type d -exec chmod 700 {} \\;`,
    ])
  }

  return _mountedSecretDirectory
}
