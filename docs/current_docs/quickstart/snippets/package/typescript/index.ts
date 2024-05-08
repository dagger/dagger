import { dag, Container, Directory, object, func } from '@dagger.io/dagger'

@object()
class HelloDagger {
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
      .container()
      .from('node:21-slim')
      .withDirectory('/src', source.withoutDirectory('dagger'))
      .withWorkdir('/src')
      .withExec(['npm', 'install'])
      .withExec(['npm', 'run', 'build'])
      .directory('./dist')
  }

  /**
   * Returns the result of running unit tests
   */
  @func()
  async test(source: Directory): Promise<string> {
    return dag
      .container()
      .from('node:21-slim')
      .withDirectory('/src', source.withoutDirectory('dagger'))
      .withWorkdir('/src')
      .withExec(['npm', 'install'])
      .withExec(['npm', 'run', 'test:unit', 'run'])
      .stdout()
  }
}
