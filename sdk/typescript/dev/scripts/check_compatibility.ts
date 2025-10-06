import { parseArgs } from "jsr:@std/cli/parse-args"
import { Markdown } from 'https://deno.land/x/deno_markdown/mod.ts';

type ModuleList = Array<{
  Path: string
}>

type TestResult = {
  moduleURL: string
  currentVersion: boolean
  latestVersion: boolean
  reason: string
}

const flags = parseArgs(Deno.args, {
  boolean: ["ignore-clone", "skip-current-version"],
  default: { "ignore-clone": false, "skip-current-version": false },
})

const decoder = new TextDecoder("utf-8")
const data = await Deno.readFile("typescript_modules_list.json")
const moduleLists = JSON.parse(decoder.decode(data)) as ModuleList

function parseGitURL(url: string): { host: string; path: string } {
  const parts = url.split("/")

  return {
    host: `https://${parts[0]}/${parts[1]}/${parts[2]}`,
    path: parts.slice(3).join("/"),
  }
}

function buildMarkdown(results: TestResult[]): string {
  const markdown = new Markdown();

  markdown.table([
    ["Module", "Current Version", "Latest Version", "Reason"],
    ...results.map((result) => [
      result.moduleURL,
      result.currentVersion ? "✅" : "❌",
      result.latestVersion ? "✅" : "❌",
      result.reason,
    ]),
  ])

  return markdown.content
}

function checkModule(
  version: "main" | "latest",
  binPath: string,
  modulePath: string,
  env?: Record<string, string>,
): string | undefined {
  const daggerDevelop = new Deno.Command(binPath, {
    args: ["-m", modulePath, "develop"],
    env: env,
  })

  console.log(`Running dagger develop on module ${modulePath}`)
  const { code: daggerDevelopCode, stderr: daggerDevelopStderr } =
    daggerDevelop.outputSync()

  if (daggerDevelopCode !== 0) {
    const error = decoder.decode(daggerDevelopStderr)
    console.log("Failed to run dagger develop on module", error)
    return `Failed to run dagger develop with ${version}`
  }

  const daggerFunctions = new Deno.Command(binPath, {
    args: ["-m", modulePath, "functions"],
  })

  console.log(`Running dagger functions on module ${modulePath}`)
  const { code: daggerFunctionsCode, stderr: daggerFunctionsStderr } =
    daggerFunctions.outputSync()

  if (daggerFunctionsCode !== 0) {
    const error = decoder.decode(daggerFunctionsStderr)
    console.log("Failed to run dagger functions on module", error)
    return `Failed to run dagger functions with ${version}`
  }

  return undefined
}

const results = await Promise.all(
  moduleLists.map((module): TestResult => {
    console.log(`Checking module ${module.Path} with latest dagger version`)

    const { host, path } = parseGitURL(module.Path)
    const repoPath = host.replace("https://", "./tmp/dagger-test-modules/")
    const modulePath = `${repoPath}/${path}`

    if (!flags["ignore-clone"]) {
      console.log(`Cloning module from repo ${host}`)
      const cloneCmd = new Deno.Command("git", {
        args: ["clone", host, repoPath],
      })

      const { code, stderr } = cloneCmd.outputSync()
      if (code !== 0) {
        const error = decoder.decode(stderr)

        if (error.includes("already exists")) {
          console.debug("Module already exists, skipping cloning")
        } else {
          return{
            moduleURL: module.Path,
            currentVersion: false,
            latestVersion: false,
            reason: "Failed to clone",
          }
        }
      } else {
        console.debug("Cloned module successfully")
      }
    }

    if (!flags["skip-current-version"]) {
      console.debug(`Skip running dagger develop with current version`)
    } else {
      const currentResult = checkModule("main", "dagger", modulePath)
      if (currentResult) {
        return {
          moduleURL: module.Path,
          currentVersion: false,
          latestVersion: false,
          reason: currentResult,
        }
      }
    }

    // Check if the module is compatible with the latest dagger version
    console.log(`Running dagger develop with latest dagger version`)
    const latestResult = checkModule(
      "latest",
      "../../../../bin/dagger",
      modulePath,
      {
        _EXPERIMENTAL_DAGGER_CLI_BIN: "../../../../bin/dagger",
        _EXPERIMENTAL_DAGGER_RUNNER_HOST:
          "docker-container://dagger-engine.dev",
      },
    )

    return {
      moduleURL: module.Path,
      currentVersion: true,
      latestVersion: latestResult === undefined,
      reason: latestResult ?? "Success",
    }
  }),
)

console.log(buildMarkdown(results))
