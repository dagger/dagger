import { dag, Container, Directory, object, func } from "@dagger.io/dagger";

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class RuntimeDetection {
  @func()
  echoRuntime(): string {
    const isBunRuntime = typeof Bun === "object";
    return isBunRuntime ? "bun" : "node";
  }
}
