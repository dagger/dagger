import { func, object } from "../../../../decorators.js"

@object()
export class Primitives {
  @func()
  str(v: String): String {
    return v
  }

  @func()
  bool(v: Boolean): Boolean {
    return v
  }

  @func()
  integer(v: Number): Number {
    return v
  }
}
