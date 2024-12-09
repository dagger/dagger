import assert from "assert"

import { convertToPascalCase } from "../case_convertor.js"

describe("case convertor", function () {
  describe("convertToPascalCase", function () {
    it("should convert kebab-case to pascal case", function () {
      const result = convertToPascalCase("hello-world")
      assert.equal(result, "HelloWorld")
    })

    it("should convert snake-case to pascal case", function () {
      const result = convertToPascalCase("hello_world")
      assert.equal(result, "HelloWorld")
    })

    it("should convert camel case to pascal case", function () {
      const result = convertToPascalCase("helloWorld")
      assert.equal(result, "HelloWorld")
    })

    it("should convert single word to pascal case", function () {
      const result = convertToPascalCase("hello")
      assert.equal(result, "Hello")
    })

    it("should convert empty string to empty string", function () {
      const result = convertToPascalCase("")
      assert.equal(result, "")
    })

    it("should keep pascal case if already valid", function () {
      const result = convertToPascalCase("HelloWorld")
      assert.equal(result, "HelloWorld")
    })
  })
})
