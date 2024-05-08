import { dag, Container, Directory, object, func } from '@dagger.io/dagger'

@object()
class HelloDagger {
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
