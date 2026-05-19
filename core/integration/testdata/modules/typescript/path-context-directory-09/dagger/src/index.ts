import { Directory, File,object, func, argument } from "@dagger.io/dagger"
@object()
export class Test {
  @func()
  async tooHighRelativeDirPath(@argument({ defaultPath: "../../../" }) backend: Directory): Promise<string[]> {
    // The engine should throw an error
    return []
  }

  @func()
	async nonExistingPath(@argument({ defaultPath: "/invalid" }) dir: Directory): Promise<string[]> {
    // The engine should throw an error
    return []
  }

  @func()
	async tooHighRelativeFilePath(@argument({ defaultPath: "../../../file.txt" }) backend: File): Promise<string> {
    // The engine should throw an error
    return ""
  }

	@func() nonExistingFile(@argument({ defaultPath: "/invalid" }) file: File): Promise<string> {
    // The engine should throw an error
    return ""
  }
}
