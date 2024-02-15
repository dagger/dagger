import * as fs from "fs"
import * as path from "path"

/**
 * Extensions supported by the loader.
 */
const allowedExtensions = [".ts", ".mts"]

/**
 * Returns a list of path of all files in the given directory
 */
export async function listFiles(dir = "."): Promise<string[]> {
  const res = await Promise.all(
    fs.readdirSync(dir).map(async (file) => {
      const filepath = path.join(dir, file)

      // Ignore node_modules and transpiled typescript
      if (filepath.includes("node_modules") || filepath.includes("dist")) {
        return []
      }

      const stat = fs.statSync(filepath)

      if (stat.isDirectory()) {
        return await listFiles(filepath)
      }

      const ext = path.extname(filepath)
      if (allowedExtensions.find((allowedExt) => allowedExt === ext)) {
        return [`${dir}/${file}`]
      }

      return []
    }),
  )

  return res.reduce((p, c) => [...c, ...p], [])
}
