import { dag, func, object } from "@dagger.io/dagger"

@object()
export class Usage {
  @func()
  async test(): Promise<string> {
    // Because `Example` implements `Fooer`, the conversion function
    // `AsMyModuleFooer` has been generated.
    return dag.myModule().foo(dag.example().asMyModuleFooer())
  }
}
