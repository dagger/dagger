import { func, object } from "@dagger.io/dagger"

@object()
export class RuntimeDetection {
  @func()
  echoRuntime(): string {
    return "node"
  }
}
