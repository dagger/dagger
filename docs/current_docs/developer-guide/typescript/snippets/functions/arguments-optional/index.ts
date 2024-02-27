import { object, func } from "@dagger.io/dagger";

@object()
class MyModule {
  @func()
  hello(name?: string): string {
    if (name) {
      return `Hello, ${name}`;
    }
    return "Hello, world";
  }
}
