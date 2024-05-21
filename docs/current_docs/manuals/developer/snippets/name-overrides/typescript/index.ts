import { dag, Container, object, func, field } from "@dagger.io/dagger"

@object()
class MyModule {
  /**
   * Definition
   */
  @field("def")
  def_: string

  constructor(
    /**
     * Definition
     */
    def_: string = "latest"
  ) {
    this.def_ = def_
  }

  /**
   * Import the specified image
   */
  @func("import")
  import_(
    /**
     * Image ref
     */
    @field("from")
    from_: string = "alpine"
  ): Container {
    return dag.container().withLabel("definition", this.def_).from(from_)
  }
}
