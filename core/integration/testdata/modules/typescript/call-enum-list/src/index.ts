import { func, object } from "@dagger.io/dagger"

export enum Language {
  Go = "GO",
  Python = "PYTHON",
  Typescript = "TYPESCRIPT",
  PHP = "PHP",
  Elixir = "ELIXIR",
}

@object()
export class Test {
  @func()
  faves(langs: Language[] = ["GO", "PYTHON"]): string {
    return langs.join(" ")
  }

  @func()
  official(): Language[] {
    return [Language.Go, Language.Python, Language.Typescript]
  }
}
