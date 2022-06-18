name: "Core"
description: "Core Dagger Types and Actions"

actions: {
  image: {
    inputs: {
      ref: string
    }
    outputs: {
      fs: "$daggerfs"
    }
  }

  git: {
    inputs: {
      remote: string
      ref: string
    }
    outputs: {
      fs: "$daggerfs"
    }
  }

  exec: {
    inputs: {
      fs: "$daggerfs"
      dir: string
      args: [...string]
      // TODO: cannot figure out how to parse this in the CUE lib:
      // mounts: [path=string]: "$daggerfs"
      mounts: "$daggermounts"
    }
    outputs: {
      fs: "$daggerfs"
      // TODO: cannot figure out how to parse this in the CUE lib:
      // mounts: [path=string]: "$daggerfs"
      mounts: "$daggermounts"
    }
  }
}
