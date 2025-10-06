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

    it("should correctly handle numbers as spacers", function () {
      const result = convertToPascalCase("hello123world")
      assert.equal(result, "Hello123World")
    })

    it("should correctly handle number as first word", function () {
      const result = convertToPascalCase("123helloworld")
      assert.equal(result, "123Helloworld")
    })

    it("should correctly handle number as last word", function () {
      const result = convertToPascalCase("helloWorld123")
      assert.equal(result, "HelloWorld123")
    })

    it("should correctly handle special characters as spacers", function () {
      const result = convertToPascalCase("hello world")
      assert.equal(result, "HelloWorld")
    })

    it("should correctly handle mix cases", function () {
      const result = convertToPascalCase("123hello-world_here")
      assert.equal(result, "123HelloWorldHere")
    })

    it("should correctly handle multiple uppercase words", function () {
      // This will still not work in module because the generated class name would be `IntrospectionJson`
      // See https://github.com/dagger/dagger/issues/7941
      const result = convertToPascalCase("introspectionJSON")
      assert.equal(result, "IntrospectionJSON")
    })
  })
})
