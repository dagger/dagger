import { object, func } from "@dagger.io/dagger";

@object()
class Test {
  @func()
  fn(id: string): string {
    return id;
  }
}
