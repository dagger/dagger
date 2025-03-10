import { dag, object, func } from "@dagger.io/dagger";

@object()
export class Tests {
  @func()
  async all(): Promise<void> {
    await Promise.all([this.hello(), this.customGreeting()]);
  }

  @func()
  async all_manual(): Promise<void> {
    await this.hello();
    await this.customGreeting();
  }

  @func()
  hello(): Promise<void> {
    return dag
      .greeter()
      .hello("World")
      .then((value: string) => {
        if (value != "Hello, World!") {
          throw new Error("unexpected greeting");
        }

        return;
      });
  }

  @func()
  customGreeting(): Promise<void> {
    return dag
      .greeter({ greeting: "Welcome" })
      .hello("World")
      .then((value: string) => {
        if (value != "Welcome, World!") {
          throw new Error("unexpected greeting");
        }

        return;
      });
  }
}
