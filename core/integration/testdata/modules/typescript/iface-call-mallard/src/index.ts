import { object, func } from "@dagger.io/dagger"

@object()
export class Mallard {
  @func()
  quack(): string {
    return "mallard quack"
  }
}
