import { object, func, field } from "@dagger.io/dagger"
import * as crypto from "crypto"

@object()
class person {
  /**
   * Get the name of the person
   */
  @field()
  name: string

  private age: number

  job: string

  constructor(name: string, job: string, age: number) {
    this.name = name
    this.age = age
    this.job = job
  }

  /**
   * Get the identity of the person based on its personal information.
   */
  @func()
  identity(): string {
    return crypto
      .createHash("sha256")
      .update(`${this.name}-${this.job}-${this.age.toString()}`)
      .digest("hex")
  }
}
