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

    const result = scan(files, "helloWorld")
    const expected: ScanResult = {
      module: {
        description: undefined,
      },
      classes: {
        HelloWorld: {
          name: "HelloWorld",
          description: "HelloWorld class",
          fields: {},
          constructor: undefined,
          methods: {
            helloWorld: {
              name: "helloWorld",
              alias: undefined,
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
                  isVariadic: false,
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

    const result = scan(files, "foo")
    const expected: ScanResult = {
      module: {},
      classes: {},
      functions: {},
    }

    assert.deepEqual(result, expected)
  })

  it("Should supports multiple files and classes that returns classes", async function () {
    const files = await listFiles(`${rootDirectory}/multipleObjects`)

    const result = scan(files, "foo")
    const expected: ScanResult = {
      module: {
        description:
          "Foo object module\n\nCompose of bar but its file description should be ignore.",
      },
      classes: {
        Bar: {
          name: "Bar",
          description: "Bar class",
          constructor: undefined,
          fields: {},
          methods: {
            exec: {
              name: "exec",
              alias: undefined,
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
                  isVariadic: false,
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
              alias: undefined,
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

    const result = scan(files, "hello-world")
    const expected: ScanResult = {
      module: {
        description: "HelloWorld module with private things",
      },
      classes: {
        HelloWorld: {
          name: "HelloWorld",
          description: "HelloWorld class",
          constructor: undefined,
          fields: {},
          methods: {
            greeting: {
              name: "greeting",
              alias: undefined,
              returnType: { kind: TypeDefKind.StringKind },
              description: "",
              args: {
                name: {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                  isVariadic: false,
                },
              },
            },
            helloWorld: {
              name: "helloWorld",
              alias: undefined,
              returnType: { kind: TypeDefKind.StringKind },
              description: "",
              args: {
                name: {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                  isVariadic: false,
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

    const result = scan(files, "alpine")
    const expected: ScanResult = {
      module: {
        description:
          "An Alpine Module for testing purpose only.\n\nWarning: Do not reproduce in production.",
      },
      classes: {
        Alpine: {
          name: "Alpine",
          description: "Alpine module",
          constructor: undefined,
          fields: {
            packages: {
              name: "packages",
              alias: undefined,
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
              alias: undefined,
              typeDef: {
                kind: TypeDefKind.ObjectKind,
                name: "Container",
              },
              description: "",
              isExposed: true,
            },
            version: {
              name: "version",
              alias: undefined,
              typeDef: { kind: TypeDefKind.StringKind },
              description: "",
              isExposed: false,
            },
            user: {
              name: "user",
              alias: undefined,
              typeDef: { kind: TypeDefKind.StringKind },
              description: "",
              isExposed: false,
            },
          },
          methods: {
            base: {
              name: "base",
              alias: undefined,
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
                  isVariadic: false,
                },
              },
            },
            install: {
              name: "install",
              alias: undefined,
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
                  isVariadic: false,
                },
              },
            },
            exec: {
              name: "exec",
              alias: undefined,
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
                  isVariadic: false,
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

    const result = scan(files, "helloWorld")
    const expected: ScanResult = {
      module: {
        description: undefined,
      },
      classes: {
        HelloWorld: {
          name: "HelloWorld",
          description: "HelloWorld class",
          fields: {},
          constructor: undefined,
          methods: {
            helloWorld: {
              name: "helloWorld",
              alias: undefined,
              returnType: { kind: TypeDefKind.StringKind },
              description: "",
              args: {
                name: {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: true,
                  defaultValue: undefined,
                  isVariadic: false,
                },
              },
            },
            isTrue: {
              name: "isTrue",
              alias: undefined,
              returnType: { kind: TypeDefKind.BooleanKind },
              description: "",
              args: {
                value: {
                  name: "value",
                  typeDef: { kind: TypeDefKind.BooleanKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                  isVariadic: false,
                },
              },
            },
            add: {
              name: "add",
              alias: undefined,
              returnType: { kind: TypeDefKind.IntegerKind },
              description: "",
              args: {
                a: {
                  name: "a",
                  typeDef: { kind: TypeDefKind.IntegerKind },
                  description: "",
                  optional: true,
                  defaultValue: "0",
                  isVariadic: false,
                },
                b: {
                  name: "b",
                  typeDef: { kind: TypeDefKind.IntegerKind },
                  description: "",
                  optional: true,
                  defaultValue: "0",
                  isVariadic: false,
                },
              },
            },
            sayBool: {
              name: "sayBool",
              alias: undefined,
              returnType: { kind: TypeDefKind.BooleanKind },
              description: "",
              args: {
                value: {
                  name: "value",
                  typeDef: { kind: TypeDefKind.BooleanKind },
                  description: "",
                  optional: true,
                  defaultValue: "false",
                  isVariadic: false,
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

    const result = scan(files, "helloWorld")
    const expected: ScanResult = {
      module: {
        description: undefined,
      },
      classes: {
        HelloWorld: {
          name: "HelloWorld",
          description: "HelloWorld class",
          constructor: undefined,
          fields: {},
          methods: {
            helloWorld: {
              name: "helloWorld",
              alias: undefined,
              returnType: { kind: TypeDefKind.VoidKind },
              description: "",
              args: {
                name: {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                  isVariadic: false,
                },
              },
            },
            asyncHelloWorld: {
              name: "asyncHelloWorld",
              alias: undefined,
              returnType: { kind: TypeDefKind.VoidKind },
              description: "",
              args: {
                name: {
                  name: "name",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: true,
                  defaultValue: undefined,
                  isVariadic: false,
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

    const result = scan(files, "helloWorld")
    const expected: ScanResult = {
      module: {
        description: "Constructor module",
      },
      classes: {
        HelloWorld: {
          name: "HelloWorld",
          description: "HelloWorld class",
          fields: {
            name: {
              description: "",
              isExposed: false,
              name: "name",
              alias: undefined,
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
                isVariadic: false,
              },
            },
          },
          methods: {
            sayHello: {
              name: "sayHello",
              alias: undefined,
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
                  isVariadic: false,
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

  it("Should correctly scan variadic arguments", async function () {
    const files = await listFiles(`${rootDirectory}/variadic`)

    const result = scan(files, "Variadic")
    const expected: ScanResult = {
      module: {
        description: undefined,
      },
      classes: {
        Variadic: {
          name: "Variadic",
          description: "",
          fields: {},
          constructor: undefined,
          methods: {
            fullVariadicStr: {
              name: "fullVariadicStr",
              alias: undefined,
              returnType: {
                kind: TypeDefKind.StringKind,
              },
              description: "",
              args: {
                vars: {
                  name: "vars",
                  typeDef: {
                    kind: TypeDefKind.ListKind,
                    typeDef: { kind: TypeDefKind.StringKind },
                  },
                  description: "",
                  optional: true,
                  defaultValue: undefined,
                  isVariadic: true,
                },
              },
            },
            semiVariadicStr: {
              name: "semiVariadicStr",
              alias: undefined,
              returnType: {
                kind: TypeDefKind.StringKind,
              },
              description: "",
              args: {
                separator: {
                  name: "separator",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                  isVariadic: false,
                },
                vars: {
                  name: "vars",
                  typeDef: {
                    kind: TypeDefKind.ListKind,
                    typeDef: { kind: TypeDefKind.StringKind },
                  },
                  description: "",
                  optional: true,
                  defaultValue: undefined,
                  isVariadic: true,
                },
              },
            },
            fullVariadicNum: {
              name: "fullVariadicNum",
              alias: undefined,
              returnType: {
                kind: TypeDefKind.IntegerKind,
              },
              description: "",
              args: {
                vars: {
                  name: "vars",
                  typeDef: {
                    kind: TypeDefKind.ListKind,
                    typeDef: { kind: TypeDefKind.IntegerKind },
                  },
                  description: "",
                  optional: true,
                  defaultValue: undefined,
                  isVariadic: true,
                },
              },
            },
            semiVariadicNum: {
              name: "semiVariadicNum",
              alias: undefined,
              returnType: {
                kind: TypeDefKind.IntegerKind,
              },
              description: "",
              args: {
                mul: {
                  name: "mul",
                  typeDef: { kind: TypeDefKind.IntegerKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                  isVariadic: false,
                },
                vars: {
                  name: "vars",
                  typeDef: {
                    kind: TypeDefKind.ListKind,
                    typeDef: { kind: TypeDefKind.IntegerKind },
                  },
                  description: "",
                  optional: true,
                  defaultValue: undefined,
                  isVariadic: true,
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

  it("Should correctly scan alias", async function () {
    const files = await listFiles(`${rootDirectory}/alias`)

    const result = scan(files, "HelloWorld")
    const expected: ScanResult = {
      module: {
        description: undefined,
      },
      classes: {
        HelloWorld: {
          name: "HelloWorld",
          description: "",
          fields: {},
          constructor: undefined,
          methods: {
            testBar: {
              name: "bar",
              alias: "testBar",
              returnType: {
                kind: TypeDefKind.ObjectKind,
                name: "Bar",
              },
              description: "",
              args: {},
            },
            bar: {
              name: "customBar",
              alias: "bar",
              returnType: {
                kind: TypeDefKind.ObjectKind,
                name: "Bar",
              },
              description: "",
              args: {
                baz: {
                  name: "baz",
                  typeDef: { kind: TypeDefKind.StringKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                  isVariadic: false,
                },
                foo: {
                  name: "foo",
                  typeDef: { kind: TypeDefKind.IntegerKind },
                  description: "",
                  optional: false,
                  defaultValue: undefined,
                  isVariadic: false,
                },
              },
            },
            greet: {
              name: "helloWorld",
              alias: "greet",
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
                  isVariadic: false,
                },
              },
            },
          },
        },
        Bar: {
          name: "Bar",
          description: "",
          fields: {
            bar: {
              name: "baz",
              alias: "bar",
              typeDef: { kind: TypeDefKind.StringKind },
              description: "",
              isExposed: true,
            },
            oof: {
              name: "foo",
              alias: "oof",
              typeDef: { kind: TypeDefKind.IntegerKind },
              description: "",
              isExposed: true,
            },
          },
          constructor: {
            args: {
              baz: {
                name: "baz",
                typeDef: { kind: TypeDefKind.StringKind },
                description: "",
                defaultValue: '"baz"',
                optional: true,
                isVariadic: false,
              },
              foo: {
                name: "foo",
                typeDef: { kind: TypeDefKind.IntegerKind },
                description: "",
                defaultValue: "4",
                optional: true,
                isVariadic: false,
              },
            },
          },
          methods: {
            zoo: {
              name: "za",
              alias: "zoo",
              returnType: {
                kind: TypeDefKind.StringKind,
              },
              description: "",
              args: {},
            },
          },
        },
      },
      functions: {},
    }

    assert.deepEqual(result, expected)
  })
})
