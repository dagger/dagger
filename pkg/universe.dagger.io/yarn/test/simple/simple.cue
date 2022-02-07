package yarn

import (
	"dagger.io/dagger"
	"universe.dagger.io/yarn"
)

dagger.#Plan & {
	// Inherit from base
	inputs: directories: testdata: _

	actions: {
		"Simple build": {
			build: yarn.#Build & {
				source: inputs.directories.testdata.contents
				// FIXME: make 'cache' optional
				cache: dagger.#CacheDir & {
					id: "yarn cache"
				}
			}
			// FIXME: verify contents
		}

		// FIXME: this test fails because CUE forbids stacking default values.
		//  "Simple build with custom image": {
		//   base: docker.#Build & {
		//    steps: [
		//     docker.#Pull & {
		//      source: "debian"
		//     },
		//     docker.#Run & {
		//      command: {
		//       name: "sh"
		//       flags: "-c": """
		//        apt-get update
		//        apt-get install -y nodejs npm
		//        npm install -g yarn
		//        """
		//      }
		//     },
		//    ]
		//   }
		//   testImage: docker.#Run & {
		//    image: base.output
		//    command: {
		//     name: "sh"
		//     flags: "-c": "yarn --version > /out.txt"
		//    }
		//    export: files: "/out.txt": _
		//   }
		//   yarnVersion: testImage.export.files["/out.txt"].contents
		//   build: yarn.#Build & {
		//    source: inputs.directories.testdata.contents
		//    cache: dagger.#CacheDir & {
		//     id: "yarn cache"
		//    }
		//    // FIXME: this would be less awkward if docker.#Run were embedded instead of wrapped
		//    run: container: image: base.output
		//   }
		//  }
	}
}
