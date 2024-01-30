import assert from "assert"
import { describe, it } from "mocha"

import { isMainObject } from "../scanner/utils.js"

describe("isMainObject tests", function () {
  it("should return false if the class is not the main object of the module", function () {
    const className = "OtherClass"
    const moduleName = "my_module"

    const result = isMainObject(className, moduleName)
    assert.equal(result, false)
  })

  it("should handle empty module names", function () {
    const className = "MyModule"
    const moduleName = ""

    const result = isMainObject(className, moduleName)
    assert.equal(result, false)
  })

  it("should handle snake_case module names", function () {
    const className = "MyModule"
    const moduleName = "my_module"

    const result = isMainObject(className, moduleName)
    assert.equal(result, true)
  })

  it("should handle kebab-case module names", function () {
    const className = "MyModule"
    const moduleName = "my-module"

    const result = isMainObject(className, moduleName)
    assert.equal(result, true)
  })

  it("should handle camelCase module names", function () {
    const className = "MyModule"
    const moduleName = "myModule"

    const result = isMainObject(className, moduleName)
    assert.equal(result, true)
  })

  it("should handle PascalCase module names", function () {
    const className = "MyModule"
    const moduleName = "MyModule"

    const result = isMainObject(className, moduleName)
    assert.equal(result, true)
  })
})
