import { object, func } from "@dagger.io/dagger";

@object()
export class Test {
  @func()
  fn(id: string): string {
    return id;
  }
}
