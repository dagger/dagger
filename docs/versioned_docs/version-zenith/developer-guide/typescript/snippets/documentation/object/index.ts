import { dag, object, func } from '@dagger.io/dagger';

/**
 * The HelloWorld object is a simple example of documenting an object.
 */
@object()
class HelloWorld {
  @func()
  async version(): Promise<string> {
    return await dag
      .container()
      .from('alpine:3.14.0')
      .withExec(['/bin/sh', '-c', 'cat /etc/os-release | grep VERSION_ID'])
      .stdout();
  }
}
