import { analysis } from "./analysis/analysis.js"
import { listFiles } from "./utils/files.js"

async function main() {
  let directory = "."

  if (process.argv.length == 3) {
    directory = process.argv[2]
  }

  const files = await listFiles(directory)

  // Analysis files
  analysis(files)
}

main()
