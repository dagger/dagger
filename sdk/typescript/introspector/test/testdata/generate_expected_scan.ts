import * as fs from "fs"
import * as path from "path"
import { fileURLToPath } from "url"

import { scan } from "../../scanner/scan.js"
import { listFiles } from "../../utils/files.js"

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const expectedFilename = "expected.json"
const diffExpectedFileName = "expected.diff.json"

async function generateExpectedScan() {
  console.info(`Generating expected scan file from directory: ${__dirname}`)

  for (const entry of fs.readdirSync(__dirname)) {
    if (!fs.lstatSync(path.join(__dirname, entry)).isDirectory()) {
      continue
    }

    console.info(`* Generating expected scan file for directory: ${entry}`)
    const files = await listFiles(`${__dirname}/${entry}`)
    const result = scan(files, `${entry}`)

    const expectedPath = path.join(__dirname, entry, expectedFilename)
    const diffExpectedPath = path.join(__dirname, entry, diffExpectedFileName)
    const currentExpected = fs.readFileSync(expectedPath, "utf8")

    if (currentExpected !== JSON.stringify(result, null, 2)) {
      console.log(
        `/!\\ Expected scan file for : ${path.join(entry, expectedFilename)} is different from the current result.`,
      )
      console.log(
        `/!\\ Please review the changes on ${path.join(entry, diffExpectedFileName)} and update the expected file if necessary.`,
      )
      fs.writeFileSync(diffExpectedPath, JSON.stringify(result, null, 2))
    } else {
      console.log(
        `Expected scan file for : ${path.join(entry, expectedFilename)} is up to date.`,
      )
    }

    console.log("\n")
  }
}

await generateExpectedScan()
