import Client, { connect } from "@dagger.io/dagger"

// Create a multi-build pipeline for a Go application.

// define build matrix
const oses = ["linux", "darwin"]
const arches = ["amd64", "arm64"]

// initialize dagger client
connect(async (client: Client) => {
    console.log("Building with Dagger")

    // get reference to the local project
    const src = await client
        .host()
        .directory(".")

    // create empty directory to put build outputs
    var outputs = client.directory()

    const golang = await client.container()
        // get golang image
        .from("golang:latest")
        // mount source code into golang image
        .withMountedDirectory("/src", src)
        .withWorkdir("/src")

    await Promise.all(oses.map(async (os) => {
        await Promise.all(arches.map(async (arch) => {
            // create a directory for each OS and architecture
            const path = `build/${os}/${arch}/`

            const build = golang
                // set GOARCH and GOOS in the build environment
                .withEnvVariable("GOOS", os)
                .withEnvVariable("GOARCH", arch)
                .withExec(["go", "build", "-o", path])

            // add build to outputs
            outputs = outputs.withDirectory(path, build.directory(path))
        }))
    }))

    // write build artifacts to host
    await outputs.export(".")
}, {LogOutput: process.stderr});
