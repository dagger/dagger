import { func, object } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  words(...words: string[]): string {
    return words.length.toString()
  }
}
