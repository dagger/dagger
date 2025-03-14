import { dag, object, func } from "@dagger.io/dagger";

@object()
export class Examples {
  @func()
  async all(): Promise<void> {
    await Promise.all([this.greeterHello(), this.greeter_customGreeting()]);
  }

  @func()
  greeterHello(): Promise<void> {
    return dag
      .greeter()
      .hello("World")
      .then((_: string) => {
        // Do something with the greeting

        return;
      });
  }

  @func()
  greeter_customGreeting(): Promise<void> {
    return dag
      .greeter({ greeting: "Welcome" })
      .hello("World")
      .then((_: string) => {
        // Do something with the greeting

        return;
      });
  }
}
