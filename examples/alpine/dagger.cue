name:        "Alpine"
description: "Do Alpine Things"

actions: {
  // Build an alpine image with the given packages installed
  build: {
    inputs: {
      packages: [...string]
    }

    outputs: {
      fs: "$daggerfs" // TODO: silly hack, means this is core.FSOutput type
    }
  }
}

