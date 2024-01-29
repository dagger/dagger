import assert from "assert"
import { describe, it } from "mocha"
import * as path from "path"
import { fileURLToPath } from "url"

import { TypeDefKind } from "../../api/client.gen.js"
import { scan, ScanResult } from "../scanner/scan.js"
import { listFiles } from "../utils/files.js"

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const rootDirectory = `${__dirname}/testdata`

describe("scan static TypeScript", function () {
  it("Should correctly scan a basic class with one method", async function () {
    const files = await listFiles(`${rootDirectory}/helloWorld`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: {
        HelloWorld: {
          name: "HelloWorld",
          description: "HelloWorld class",
          fields: {},
          constructor: undefined,
          methods: {
            helloWorld: {
              name: "helloWorld",
              returnType: {
                kind: TypeDefKind.StringKind,
              },
              description: "",
              args: {
                name: {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              },
            },
          },
        },
      },
      functions: {},
    }

    assert.deepEqual(result, expected)
  })

  it("Should ignore class that does not have the object decorator", async function () {
    const files = await listFiles(`${rootDirectory}/noDecorators`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: {},
      functions: {},
    }

    assert.deepEqual(result, expected)
  })

  it("Should supports multiple files and classes that returns classes", async function () {
    const files = await listFiles(`${rootDirectory}/multipleObjects`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: {
        Bar: {
          name: "Bar",
          description: "Bar class",
          constructor: undefined,
          fields: {},
          methods: {
            exec: {
              name: "exec",
              description: "Execute the command and return its result",
              returnType: { kind: TypeDefKind.StringKind },
              args: {
                cmd: {
                  name: "cmd",
                  typeDef: {
                    kind: TypeDefKind.ListKind,
                    typeDef: {
                      kind: TypeDefKind.StringKind,
                    },
                  },
                  description: "Command to execute",
                  optional: false,
                  defaultValue: undefined,
                },
              },
            },
          },
        },
        Foo: {
          name: "Foo",
          description: "Foo class",
          constructor: undefined,
          fields: {},
          methods: {
            bar: {
              name: "bar",
              description: "Return Bar object",
              returnType: {
                kind: TypeDefKind.ObjectKind,
                name: "Bar",
              },
              args: {},
            },
          },
        },
      },
      functions: {},
    }

    assert.deepEqual(result, expected)
  })

  it("Should not expose private methods from a class", async function () {
    const files = await listFiles(`${rootDirectory}/privateMethod`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: {
        HelloWorld: {
          name: "HelloWorld",
          description: "HelloWorld class",
          constructor: undefined,
          fields: {},
          methods: {
            greeting: {
              name: "greeting",
              returnType: { kind: TypeDefKind.StringKind },
              description: "",
              args: {
                name: {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              },
            },
            helloWorld: {
              name: "helloWorld",
              returnType: { kind: TypeDefKind.StringKind },
              description: "",
              args: {
                name: {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              },
            },
          },
        },
      },
      functions: {},
    }

    assert.deepEqual(result, expected)
  })

  it("should scan classes' properties to keep a state", async function () {
    const files = await listFiles(`${rootDirectory}/state`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: {
        Alpine: {
          name: "Alpine",
          description: "Alpine module",
          constructor: undefined,
          fields: {
            packages: {
              name: "packages",
              typeDef: {
                kind: TypeDefKind.ListKind,
                typeDef: {
                  kind: TypeDefKind.StringKind,
                },
              },
              description: "packages to install",
              isExposed: true,
            },
            ctr: {
              name: "ctr",
              typeDef: {
                kind: TypeDefKind.ObjectKind,
                name: "Container",
              },
              description: "",
              isExposed: true,
            },
            version: {
              name: "version",
              typeDef: { kind: TypeDefKind.StringKind },
              description: "",
              isExposed: false,
            },
            user: {
              name: "user",
              typeDef: { kind: TypeDefKind.StringKind },
              description: "",
              isExposed: false,
            },
          },
          methods: {
            base: {
              name: "base",
              returnType: {
                kind: TypeDefKind.ObjectKind,
                name: "Alpine",
              },
              description: "Returns a base Alpine container",
              args: {
                version: {
                  name: "version",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "version to use (default to: 3.16.2)",
                  optional: true,
                  defaultValue: undefined,
                },
              },
            },
            install: {
              name: "install",
              returnType: {
                kind: TypeDefKind.ObjectKind,
                name: "Alpine",
              },
              description: "",
              args: {
                pkgs: {
                  name: "pkgs",
                  typeDef: {
                    kind: TypeDefKind.ListKind,
                    typeDef: {
                      kind: TypeDefKind.StringKind,
                    },
                  },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              },
            },
            exec: {
              name: "exec",
              returnType: { kind: TypeDefKind.StringKind },
              description: "",
              args: {
                cmd: {
                  name: "cmd",
                  typeDef: {
                    kind: TypeDefKind.ListKind,
                    typeDef: {
                      kind: TypeDefKind.StringKind,
                    },
                  },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              },
            },
          },
        },
      },
      functions: {},
    }

    assert.deepEqual(result, expected)
  })

  it("Should detect optional parameters of a method", async function () {
    const files = await listFiles(`${rootDirectory}/optionalParameter`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: {
        HelloWorld: {
          name: "HelloWorld",
          description: "HelloWorld class",
          fields: {},
          constructor: undefined,
          methods: {
            helloWorld: {
              name: "helloWorld",
              returnType: { kind: TypeDefKind.StringKind },
              description: "",
              args: {
                name: {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: true,
                  defaultValue: undefined,
                },
              },
            },
            isTrue: {
              name: "isTrue",
              returnType: { kind: TypeDefKind.BooleanKind },
              description: "",
              args: {
                value: {
                  name: "value",
                  typeDef: { kind: TypeDefKind.BooleanKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              },
            },
            add: {
              name: "add",
              returnType: { kind: TypeDefKind.IntegerKind },
              description: "",
              args: {
                a: {
                  name: "a",
                  typeDef: { kind: TypeDefKind.IntegerKind },
                  description: "",
                  optional: true,
                  defaultValue: "0",
                },
                b: {
                  name: "b",
                  typeDef: { kind: TypeDefKind.IntegerKind },
                  description: "",
                  optional: true,
                  defaultValue: "0",
                },
              },
            },
            sayBool: {
              name: "sayBool",
              returnType: { kind: TypeDefKind.BooleanKind },
              description: "",
              args: {
                value: {
                  name: "value",
                  typeDef: { kind: TypeDefKind.BooleanKind },
                  description: "",
                  optional: true,
                  defaultValue: "false",
                },
              },
            },
          },
        },
      },
      functions: {},
    }

    assert.deepEqual(result, expected)
  })

  it("Should correctly handle function with void return", async function () {
    const files = await listFiles(`${rootDirectory}/voidReturn`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: {
        HelloWorld: {
          name: "HelloWorld",
          description: "HelloWorld class",
          constructor: undefined,
          fields: {},
          methods: {
            helloWorld: {
              name: "helloWorld",
              returnType: { kind: TypeDefKind.VoidKind },
              description: "",
              args: {
                name: {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              },
            },
            asyncHelloWorld: {
              name: "asyncHelloWorld",
              returnType: { kind: TypeDefKind.VoidKind },
              description: "",
              args: {
                name: {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: true,
                  defaultValue: undefined,
                },
              },
            },
          },
        },
      },
      functions: {},
    }

    assert.deepEqual(result, expected)
  })

  it("Should introspect constructor", async function () {
    const files = await listFiles(`${rootDirectory}/constructor`)

    const result = scan(files)
    const expected: ScanResult = {
      classes: {
        HelloWorld: {
          name: "HelloWorld",
          description: "HelloWorld class",
          fields: {
            name: {
              description: "",
              isExposed: false,
              name: "name",
              typeDef: {
                kind: TypeDefKind.StringKind,
              },
            },
          },
          constructor: {
            args: {
              name: {
                name: "name",
                typeDef: { kind: TypeDefKind.StringKind },
                description: "",
                defaultValue: '"world"',
                optional: true,
              },
            },
          },
          methods: {
            sayHello: {
              name: "sayHello",
              returnType: {
                kind: TypeDefKind.StringKind,
              },
              description: "",
              args: {
                name: {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                },
              },
            },
          },
        },
      },
      functions: {},
    }

    assert.deepEqual(result, expected)
  })
})
