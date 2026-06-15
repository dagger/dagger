import { Directory, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  foo: string

  @func()
  dir: Directory

  @func()
  bar: number

  @func()
  baz: string[]

  @func()
  neverSetDir?: Directory

  constructor(foo: string, dir: Directory, bar = 42, baz: string[] = []) {
    this.foo = foo
    this.dir = dir
    this.bar = bar
    this.baz = baz
  }

  @func()
  gimmeFoo(): string {
    return this.foo
  }

  @func()
  gimmeBar(): number {
    return this.bar
  }

  @func()
  gimmeBaz(): string[] {
    return this.baz
  }

  @func()
  async gimmeDirEnts(): Promise<string[]> {
    return this.dir.entries()
  }
}
