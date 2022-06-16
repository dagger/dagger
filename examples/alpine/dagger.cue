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

// TODO: just a sketch of core, doesn't need to be here
// FS: "$daggerfs"
// 
// image: {
//   inputs: {
//     ref: string
//   }
//   outputs: {
//     fs: FS
//   }
// }
// 
// git: {
//   input: {
//     remote: string
//     ref: string
//   }
//   output: {
//     fs: FS
//   }
// }
// 
// exec: {
//   input: {
//     base: FS
//     dir: string
//     args: [...string]
//     mounts: [...{
//       fs: FS
//       path: string
//     }]
//   }
//   output: {
//     fs: FS
//     mounts: [path=string]: FS
//   }
// }
