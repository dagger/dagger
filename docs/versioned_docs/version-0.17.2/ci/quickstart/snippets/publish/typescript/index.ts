import {
  dag,
  Container,
  Directory,
  object,
  func,
  argument,
} from "@dagger.io/dagger"

@object()
class HelloDagger {
  /**
   * Publish the application container after building and testing it on-the-fly
   */
  @func()
  async publish(
    @argument({ defaultPath: "/" }) source: Directory,
  ): Promise<string> {
    // call Dagger Function to run unit tests
    await this.test(source)
    // call Dagger Function to build the application image
    // publish the image to ttl.sh
    return await this.build(source).publish(
      "ttl.sh/hello-dagger-" + Math.floor(Math.random() * 10000000),
    )
  }
}
