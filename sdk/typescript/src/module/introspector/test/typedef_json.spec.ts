import assert from "assert"
import { describe, it } from "mocha"
import path from "path"
import { fileURLToPath } from "url"

import { scan } from "../index.js"
import { serializeModule } from "../typedef_json.js"
import { listFiles } from "../utils/files.js"

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const rootDirectory = `${__dirname}/testdata`

type SerializedModule = {
  objects: Record<string, { methods: Record<string, { isUp: boolean }> }>
}

describe("serializeModule", function () {
  it("marks only @start and @up functions as services", async function () {
    this.timeout(60000)
    const files = await listFiles(`${rootDirectory}/decorators`)
    const module = serializeModule(
      await scan(files, "decorators"),
    ) as SerializedModule

    const methods = module.objects["Decorators"].methods
    assert.equal(methods["startSomething"].isUp, true)
    assert.equal(methods["upSomething"].isUp, true)
    assert.equal(methods["checkSomething"].isUp, false)
    assert.equal(methods["timed"].isUp, false)
  })
})
