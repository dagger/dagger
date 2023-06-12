import fs from "fs"
import path from "path"

export async function writeFile(content: string, dest: string): Promise<void> {
  fs.writeFileSync(dest, content, { mode: 0o600 })
}

export async function readFile(path: string): Promise<string> {
  return fs.readFileSync(path, { encoding: "utf-8" })
}

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

      const allowedExtensions = [".ts", ".mts"]
      const ext = path.extname(filepath)
      if (allowedExtensions.find((allowedExt) => allowedExt === ext)) {
        return [file]
      }

      return []
    })
  )

  return res.reduce((p, c) => [...c, ...p], [])
}
