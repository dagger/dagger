import { Directory, dag } from "@dagger.io/dagger"

const sourceHostPath = `${__dirname}/../../`

export function source(): Directory {
  return dag.host().directory(sourceHostPath)
}
