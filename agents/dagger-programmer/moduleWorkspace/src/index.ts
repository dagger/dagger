/**
 * A generated module for DagWorkspace functions
 */
import {
  Container,
  dag,
  Directory,
  object,
  func,
  ReturnType,
} from "@dagger.io/dagger";

@object()
export class ModuleWorkspace {
  /*
   * The workspace directory
   */
  @func()
  workspace: Directory;
  sdk: string;
  name: string;
  daggerVersion: string;

  constructor(
    /*
     * module SDK
     */
    sdk: string,
    /*
     * module name
     */
    name: string,
    /*
     * Dagger Version
     */
    daggerVersion = "latest",
    /*
     * Workspace directory
     */
    workspace?: Directory,
  ) {
    this.sdk = sdk;
    this.name = name;
    this.daggerVersion = daggerVersion;
    this.workspace =
      workspace ??
      dag.directory().withNewFile(this.sdkFilePath(), "EMPTY FILE");
  }
  /**
   * Writes the module code to the workspace
   * @param content The updated module code
   */
  @func()
  write(content: string): ModuleWorkspace {
    // NOTE: constraining this to the default file path for now to keep things simple
    // To develop module examples or more complicated modules, this will need to be unlocked
    this.workspace = this.workspace.withNewFile(this.sdkFilePath(), content);
    return this;
  }

  /**
   * Reads the module code from the workspace
   */
  @func()
  async read(): Promise<string> {
    return await this.workspace.file(this.sdkFilePath()).contents();
  }

  /**
   * Test if a module works by outputting the module functions
   */
  @func()
  async test(): Promise<string> {
    const dagger = this.daggerCommand();
    const functions = dagger.withExec(["/usr/local/bin/dagger", "functions"], {
      experimentalPrivilegedNesting: true,
      expect: ReturnType.Any,
    });
    if ((await functions.exitCode()) != 0) {
      return await functions.stderr();
    }
    return await functions.stdout();
  }

  /**
   * Get reference information for developing with a Dagger SDK
   * @param sdk Dagger SDK language to get reference for
   */
  @func()
  async getReference(sdk: string): Promise<string> {
    const daggerDocs = dag
      .git("https://github.com/dagger/dagger")
      .head()
      .tree()
      .directory("docs/current_docs/");

    // Messing around with changing which snippets are included. Dont want to overload the LLM with too much info
    const snippets: { [key: string]: string } = {
      // "multi stage build": "/cookbook/snippets/builds/multi-stage-build",
      // "cache volumes": "/cookbook/snippets/builds/cache",
      // "secret variables": "/cookbook/snippets/secret-variable",
      "bind services to containers": "/api/snippets/services/bind-services",
      // "expose services to the host": "/api/snippets/services/expose-dagger-services-to-host",
    };

    // FIXME: ideally cookbook snippets would follow module structure too
    const snippetSdkPaths: { [key: string]: string } = {
      go: "main.go",
      python: "main.py",
      typescript: "index.ts",
      php: "src/MyModule.php",
      java: "src/main/java/io/dagger/modules/mymodule/MyModule.java",
    };

    // TODO: also include full sdk reference?
    let reference = `Reference for using Dagger with the ${sdk} SDK\n`;
    reference += `The relevant code for a ${sdk} SDK module is at "${this.sdkFilePath(sdk)}"\n`;
    for (const snippet in snippets) {
      const code = await daggerDocs
        .file(`${snippets[snippet]}/${sdk}/${snippetSdkPaths[sdk]}`)
        .contents();
      reference += `\n${snippet}:\n<code>\n${code}\n</code>\n`;
    }
    return reference;
  }

  // Helper to get the correct code path for a given SDK
  sdkFilePath(sdk?: string): string {
    sdk = sdk ?? this.sdk;
    const defaultFilePaths: { [key: string]: string } = {
      go: "main.go",
      python: `src/${this.name}/main.py`,
      typescript: "src/index.ts",
      php: `src/${this.name}.php`,
      java: `src/main/java/io/dagger/modules/${this.name.toLowerCase()}/${this.name}.java`,
    };
    return defaultFilePaths[this.sdk];
  }

  // Helper to get a container that can execute dagger commands on our module
  daggerCommand(): Container {
    return dag
      .container()
      .from("alpine")
      .withExec(["apk", "add", "curl"])
      .withEnvVariable("DAGGER_VERSION", this.daggerVersion)
      .withExec([
        "sh",
        "-c",
        "curl -fsSL https://dl.dagger.io/dagger/install.sh | BIN_DIR=/usr/local/bin sh",
      ])
      .withWorkdir("/mod")
      .withExec(["dagger", "init", "--name", this.name, "--sdk", this.sdk], {
        experimentalPrivilegedNesting: true,
      })
      .withFile(this.sdkFilePath(), this.workspace.file(this.sdkFilePath()));
  }
}
