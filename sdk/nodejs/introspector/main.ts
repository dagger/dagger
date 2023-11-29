import { scan } from "./scanner/scan.js"
import { listFiles } from "./utils/files.js"

async function main() {
  let directory = "."

  if (process.argv.length == 3) {
    directory = process.argv[2]
  }

  const files = await listFiles(directory)

  // scan files
  const result = scan(files)
  console.log(result)
}

main()
