import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
class HelloDagger {
  /**
   * Tests, builds and publishes the application
   */
  @func()
  async publish(source: Directory): Promise<string> {
    // call Dagger Function to run unit tests
    this.test(source)
    // call Dagger Function to build the application image
    // publish the image to ttl.sh
    return await this.build(source).publish(
      "ttl.sh/myapp-" + Math.floor(Math.random() * 10000000),
    )
  }
}
