import crypto from "crypto"

import { object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func({ cache: "40s" })
  testTtl(): string {
    return crypto.randomBytes(16).toString("hex")
  }

  @func({ cache: "session" })
  testCachePerSession(): string {
    return crypto.randomBytes(16).toString("hex")
  }

  @func({ cache: "never" })
  testNeverCache(): string {
    return crypto.randomBytes(16).toString("hex")
  }

  @func()
  testAlwaysCache(): string {
    return crypto.randomBytes(16).toString("hex")
  }
}
