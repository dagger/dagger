import { describe, it } from "mocha"
import assert from "node:assert/strict"

import { loadParentState, loadResult } from "../../entrypoint/load.js"
import { Executor } from "../../executor.js"
import { scan } from "../index.js"
import { listFiles } from "../utils/files.js"

describe("Collection state", () => {
  it("preserves the base through copies without a delta value", async () => {
    const files = await listFiles(
      new URL("./testdata/collections", import.meta.url).pathname,
    )
    const module = await scan(files, "collections")
    const object = module.objects.Items
    const executor = new Executor([], module)
    const state = await loadParentState(executor, object as never, {
      parentName: "Items",
      fnName: "selected",
      fnArgs: {},
      parentArgs: {
        names: ["a", "b"],
        prefix: "item:",
        __daggerCollectionBase: "original",
      },
    })
    const copy = { ...state, names: ["b", "c"] }
    assert.deepEqual(await loadResult(copy, module, object), {
      names: ["b", "c"],
      prefix: "item:",
      __daggerCollectionBase: "original",
    })
    assert.deepEqual(
      await loadResult({ names: ["c"], prefix: "item:" }, module, object),
      {
        names: ["c"],
        prefix: "item:",
      },
    )
    assert.equal(object.properties.__daggerCollectionBase, undefined)
  })
})
