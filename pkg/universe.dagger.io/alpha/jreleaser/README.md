## JReleaser
This package lets you release projects with [JReleaser](https://jreleaser.org).

### Usage
You may use this package in two ways depending on how secrets are passed to the tool.

**1. Environment Variables**
This mode expects secrets to be set as environment variables, set as explicitly arguments on the chosen JReleaser command such as:


```
package main

import (
    "dagger.io/dagger"
    "dagger.io/dagger/core"
    "universe.dagger.io/alpha/jreleaser"
)

dagger.#Plan & {
    actions: {
        release: jreleaser.#FullRelease & {
            source: _source.output
            _source: core.#Source & {
	            path: "."
	        }
            env: {
                JRELEASER_PROJECT_VERSION: "1.0.0"
                JRELEASER_GITHUB_TOKEN: "value-of-gh-token"
            }
        }
    }
}
```

**2. Secrets File**
This mode expects secrets to be set as an external file as explained [here](https://jreleaser.org/guide/latest/configuration/environment.html).
You may define additional environment variables as needed.


```
package main

import (
    "dagger.io/dagger"
    "dagger.io/dagger/core"
    "universe.dagger.io/alpha/jreleaser"
)

dagger.#Plan & {
    client: filesystem: "~/.jreleaser": read: {
        contents: dagger.#FS
    }
    
    actions: {
        release: jreleaser.#FullRelease & {
            source: _source.output
            _source: core.#Source & {
	            path: "."
	        }
            jreleaser_home: client.filesystem."~/.jreleaser".read.contents
            env: {
                JRELEASER_PROJECT_VERSION: "1.0.0"
            }
        }
    }
}
```

### Commands
The following is the list of commands you may invoke from the `jreleaser` package:

 - #Download
 - #Assemble
 - #Changelog
 - #Checksum
 - #Sign
 - #Upload
 - #Release
 - #Prepare
 - #Package
 - #Publish
 - #Announce
 - #FullRelease
 
### Version
The value of `version` may be any of the tags listed at [jreleaser/jreleaser-slim](https://hub.docker.com/repository/docker/jreleaser/jreleaser-slim/tags?page=1&ordering=last_updated)
with `latest` being the default value. Tag `latest` resolves to the latest stable release while `early-access` resolves to the latest snapshot release. Both `latest` and `early-access` hamper reproducible builds as they are moving targets, use them with caution.

### Inputs
Commands accept the following inputs:

 - **source: dagger.#FS**
   Project sources
   
 - **jreleaser_home?: dagger.#FS**
   Location of JReleaser secrets file
 
 - **version: string | \*"latest"**
   JReleaser version to use
 
 - **args: [...string]**
   Command arguments
 
 - **flags: [string]: (string | true)**
   Command flags
   
 - **env: [string]: string | dagger.#Secret**
   Environment variables

### Outputs
Every command produces the following outputs

 - **output**
   The image used to run the command
   
 - **outputDir**
   The output directory (as dagger.#FS) used by JReleaser, typically `out/jreleaser`.
 
 - **outputLog**
   The contents (as string) of the `out/jreleaser/trace.log` file.
 
 - **outputProps**
   The contents (as string) of the `out/jreleaser/output.properties` file.
