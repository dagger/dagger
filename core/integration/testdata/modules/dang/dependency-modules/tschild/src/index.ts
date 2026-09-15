import { object, func } from "@dagger.io/dagger"

@object()
export class Tschild {
  @func()
  value(): string {
    return "typescript"
  }
}
