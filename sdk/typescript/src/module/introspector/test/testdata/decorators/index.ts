import {
  Changeset,
  Container,
  Directory,
  File,
  Service,
} from "../../../../../api/client.gen.js"
import {
  argument,
  check,
  field,
  func,
  generate,
  object,
  up,
} from "../../../../decorators.js"

// Module-level constants referenced from decorators. They exercise the decorator
// evaluator: it must resolve referenced symbols, not only inlined literals.
const IGNORE = ["foo", "ref", "bar"]
const DEFAULT_PATH = "/src"
const DEFAULT_ADDRESS = "alpine:3.21"
const ALIAS = "renamedReference"
const CACHE = "session"

@object()
export class Decorators {
  /**
   * A field exposed with the deprecated `@field` decorator.
   */
  @field()
  version: string = "1.0.0"

  /**
   * A field exposed with `@func` using a string alias.
   */
  @func("aliasedField")
  name: string = "dagger"

  // --- @argument: defaultPath ---

  @func()
  withDefaultPath(
    @argument({ defaultPath: "." })
    source: Directory,
  ): Directory {
    return source
  }

  @func()
  withReferencedDefaultPath(
    @argument({ defaultPath: DEFAULT_PATH })
    source: Directory,
  ): Directory {
    return source
  }

  @func()
  withFileDefaultPath(
    @argument({ defaultPath: "./README.md" })
    file: File,
  ): File {
    return file
  }

  // --- @argument: ignore ---

  @func()
  withInlinedIgnore(
    @argument({ ignore: ["foo", "bar", "baz"] })
    source: Directory,
  ): Directory {
    return source
  }

  @func()
  withReferencedIgnore(
    @argument({ ignore: IGNORE })
    source: Directory,
  ): Directory {
    return source
  }

  // --- @argument: defaultPath and ignore combined (inlined + referenced) ---

  @func()
  withDefaultPathAndIgnore(
    @argument({ defaultPath: ".", ignore: IGNORE })
    source: Directory,
  ): Directory {
    return source
  }

  // --- @argument: defaultAddress (Container) ---

  @func()
  withDefaultAddress(
    @argument({ defaultAddress: "alpine:3.21" })
    ctr: Container,
  ): Container {
    return ctr
  }

  @func()
  withReferencedDefaultAddress(
    @argument({ defaultAddress: DEFAULT_ADDRESS })
    ctr: Container,
  ): Container {
    return ctr
  }

  // --- @func: alias ---

  @func("stringAlias")
  withStringAlias(): string {
    return "aliased"
  }

  @func({ alias: "objectAlias" })
  withObjectAlias(): string {
    return "aliased"
  }

  @func({ alias: ALIAS })
  withReferencedAlias(): string {
    return "aliased"
  }

  // --- @func: cache ---

  @func({ cache: "never" })
  withCache(): string {
    return "cached"
  }

  @func({ alias: "cachedAlias", cache: CACHE })
  withCacheAndAlias(): string {
    return "cached"
  }

  // --- @check / @generate / @up markers (combined with @func) ---

  @func()
  @check()
  checkSomething(): void {}

  @func()
  @generate()
  generateSomething(): Changeset {
    throw new Error("not implemented")
  }

  @func()
  @up()
  upSomething(): Service {
    throw new Error("not implemented")
  }
}
