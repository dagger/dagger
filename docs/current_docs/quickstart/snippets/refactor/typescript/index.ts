import { dag, Container, Directory, object, func } from '@dagger.io/dagger'

@object()
class HelloDagger {
  /**
   * Tests, builds, packages and publishes the application
   */
  @func()
  async ci(source: Directory): Promise<string> {
    // run tests
    this.test(source)
    // obtain the build output directory
    let build = this.build(source)
    // create and publish a container with the build output
    return await this.package(build).publish('ttl.sh/myapp-' + Math.floor(Math.random() * 10000000))
  }

  /**
   * Returns a container with the production build
   */
  @func()
  package(build: Directory): Container {
    return dag
      .container()
      .from('nginx:1.25-alpine')
      .withDirectory('/usr/share/nginx/html', build)
      .withExposedPort(80)
  }

  /**
   * Returns a directory with the production build
   */
  @func()
  build(source: Directory): Directory {
    return dag
      .node({ version: '21' })
      .withNpm()
      .withSource(source)
      .install([])
      .commands()
      .run(['build'])
      .directory('./dist')
  }

  /**
   * Returns the result of running unit tests
   */
  @func()
  async test(source: Directory): Promise<string> {
    return dag
      .node({ version: '21' })
      .withNpm()
      .withSource(source)
      .install([])
      .commands()
      .run(['test:unit', 'run'])
      .stdout()
  }
}
