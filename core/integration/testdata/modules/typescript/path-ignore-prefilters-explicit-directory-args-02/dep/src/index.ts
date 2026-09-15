import { object, func, Directory, argument } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  call(
    @argument({ ignore: ["foo.txt", "bar"] }) dir: Directory,
  ): Directory {
    return dir
  }
}