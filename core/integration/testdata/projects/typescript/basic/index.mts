import fs from "fs"
import Client, { serveCommands, File, Directory } from "@dagger.io/dagger"

serveCommands(testFile, testDir, testExportLocalDir, testImportedProjectDir)

function testFile(client: Client, prefix: string): File {
  const name = prefix + "foo.txt"

  return client.directory().withNewFile(name, "foo\n").file(name)
}

function testDir(client: Client, prefix: string): Directory {
  return client.directory()
    .withNewDirectory(prefix + "subdir")
    .withNewFile(prefix + "subdir/subbar1.txt", "subbar1\n")
    .withNewFile(prefix + "subdir/subbar2.txt", "subbar2\n")
    .withNewFile(prefix + "bar1.txt", "bar1\n")
    .withNewFile(prefix + "bar2.txt", "bar2\n")
}

function testExportLocalDir(client: Client): Directory {
  return client.host().directory("./core/integration/testdata/projects/typescript/basic")
}

function testImportedProjectDir(client: Client): string {
    return fs.readdirSync(".").join("\n")
}