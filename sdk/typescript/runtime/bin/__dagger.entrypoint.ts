// THIS FILE IS AUTO GENERATED. PLEASE DO NOT EDIT.
import { entrypoint } from "@dagger.io/dagger"
import * as fs from "fs"
import * as path from "path"

const allowedExtensions = [".ts", ".mts"]

function listTsFilesInModule(dir = import.meta.dirname): string[] {
  const res = fs.readdirSync(dir).map((file) => {
    const filepath = path.join(dir, file)

    const stat = fs.statSync(filepath)

    if (stat.isDirectory()) {
      return listTsFilesInModule(filepath)
    }

    const ext = path.extname(filepath)
    if (allowedExtensions.find((allowedExt) => allowedExt === ext)) {
      return [path.join(dir, file)]
    }

    return []
  })

  return res.reduce(
    (p, c) => [...c, ...p],
    [`${import.meta.dirname}/../sdk/src/api/client.gen.ts`],
  )
}

const files = listTsFilesInModule()

entrypoint(files)
