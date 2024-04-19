# BuildKit Developer Docs

These are the BuildKit developer docs, designed to be read by technical users
interested in contributing to or integrating with BuildKit.

> **Warning**
>
> While these docs attempt to keep up with the current state of our `master`
> development branch, the code is constantly changing and updating, as bugs are
> fixed, and features are added. Remember, the ultimate source of truth is
> always the code base.


## Video

You can find recording for "BuildKit architecture and internals" session in [here](https://drive.google.com/file/d/1zGMQipL5WJ3sLySu7gHZ_o6tFpxRXRHs/view) ([slides](https://docs.google.com/presentation/d/1tEnuMOENuoVQ3l6viBmguYUn7XpjIHIC-3RHzfyIgjc/edit?usp=sharing)). This session gives an overview how BuildKit works under the hood and how it was designed. If you’re thinking about contributing to BuildKit, this session should give you an overview of the most important components that make up BuildKit and how they work together.

## Jargon

The following terms are often used throughout the codebase and the developer
documentation to describe different components and processes in the image build
process.

| Name | Description |
| :--- | :---------- |
| **LLB** | LLB stands for low-level build definition, which is a binary intermediate format used for defining the dependency graph for processes running part of your build. |
| **Definition** | Definition is the LLB serialized using protocol buffers. This is the protobuf type that is transported over the gRPC interfaces. |
| **Frontend** | Frontends are builders of LLB and may issue requests to Buildkit’s gRPC server like solving graphs. Currently there is only `dockerfile.v0` and `gateway.v0` implemented, but the gateway frontend allows running container images that function as frontends.  |
| **State** | State is a helper object to build LLBs from higher level concepts like images, shell executions, mounts, etc. Frontends use the state API in order to build LLBs and marshal them into the definition. |
| **Solver** | Solver is an abstract interface to solve a graph of vertices and edges to find the final result. An LLB solver is a solver that understands that vertices are implemented by container-based operations, and that edges map to container-snapshot results. |
| **Vertex** | Vertex is a node in a build graph. It defines an interface for a content addressable operation and its inputs. |
| **Op** | Op defines how the solver can evaluate the properties of a vertex operation. An op is retrieved from a vertex and executed in the worker. For example, there are op implementations for image sources, git sources, exec processes, etc. |
| **Edge** | Edge is a connection point between vertices. An edge references a specific output a vertex’s operation. Edges are used as inputs to other vertices. |
| **Result** | Result is an abstract interface return value of a solve. In LLB, the result is a generic interface over a container snapshot. |
| **Worker** | Worker is a backend that can run OCI images. Currently, Buildkit can run with workers using either runc or containerd. |

## Table of Contents

The developer documentation is split across various files.

For an overview of the process of building images:

- [Request lifecycle](./request-lifecycle.md) - observe how incoming requests
  are solved to produce a final artifact.
- [Dockerfile to LLB](./dockerfile-llb.md) - understand how `Dockerfile`
  instructions are converted to the LLB format.
- [Solver](./solver.md) - understand how LLB is evaluated by the solver to
  produce the solve graph.

We also have a number of more specific guides:

- [MergeOp and DiffOp](./merge-diff.md) - learn how MergeOp and DiffOp are
  implemented, and how to program with them in LLB.

There are also guides on specific ways of working on the buildkit repository:

- [Remote Debugging Guide](./remote-debugging.md) - learn how to utilize the debugger when running buildkit in docker.
