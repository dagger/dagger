import * as fs from "fs"
import * as process from "process"

type Runtime = "bun" | "node" | undefined

const packageRuntime = readPackageJson()
if (packageRuntime !== undefined) {
  process.stdout.write(packageRuntime)
  process.exit(0)
}

const lockfileRuntime = detectLockfile()
if (lockfileRuntime !== undefined) {
  process.stdout.write(lockfileRuntime)
  process.exit(0)
}

process.stdout.write("node")
process.exit(0)

function readPackageJson(): Runtime {
  const packagePath = `./package.json`

  if (!fs.existsSync(packagePath)) {
    return undefined
  }

  const packageFile = fs.readFileSync(packagePath, "utf8")

  let packageJson
  try {
    packageJson = JSON.parse(packageFile)
  } catch (e) {
    // unable to parse package.json
    return undefined
  }

  if (packageJson?.dagger?.runtime !== undefined) {
    return packageJson.dagger.runtime
  }

  return undefined
}

function detectLockfile(): Runtime {
  const packageLockPath = "./package-lock.json"
  const bunLockPath = "./bun.lockb"

  if (fs.existsSync(packageLockPath)) {
    return "node"
  }

  if (fs.existsSync(bunLockPath)) {
    return "bun"
  }

  return undefined
}
