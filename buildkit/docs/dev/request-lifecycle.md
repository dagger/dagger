# Solve Request Lifecycle

Buildkit solves build graphs to find the final result. By default, nothing will
be exported to the client, but requests can be made after solving the graph to
export results to external destinations (like the client’s filesystem).

A solve request goes through the following:

1. Client makes a solve request and sends it to buildkitd over gRPC. The
   request may either include a LLB definition, or the name of a frontend (must
   be `dockerfile.v0` or `gateway.v0`), but it must not be both.
2. Buildkitd receives the solve request with the Controller. The controller is
   registered as the ControlServer gRPC service.
3. The controller passes it down to the LLB solver, which will create a job for
   this request. It will also create a FrontendLLBBridge, that provides a
   solving interface over the job object.
4. The request is processed:
   - If the request is definition-based, it will simply build the definition.
   - If the request is frontend-based, it will run the frontend over the
     gateway while passing it a reference to the FrontendLLBBridge. Frontends
     must return a result for the solve request, but they may also issue solve
     requests themselves to the bridge.
5. The results are plumbed back to the client, and the temporary job and bridge
   are discarded.

<!-- Diagram from https://gist.github.com/hinshun/9618a3c4b64b3bb16864603ef332f3a5 --->
```mermaid
sequenceDiagram
    ControlClient ->> ControlServer : Solve
    ControlServer ->> Solver : Solve

    Solver ->> Job : Create job
    activate Job

    Solver ->> FrontendLLBBridge : Create bridge over Job
    activate FrontendLLBBridge

    Solver ->> FrontendLLBBridge : Solve

    alt definition-based solve
        FrontendLLBBridge ->> Job : Build
        activate Job
        Job -->> FrontendLLBBridge : Result
        deactivate Job
    else frontend-based solve
        FrontendLLBBridge ->> Frontend : Solve
        activate Frontend
        note over FrontendLLBBridge, Frontend : Frontend must be either <br/>dockerfile.v0 or gateway.v0.

        loop
            Frontend ->> FrontendLLBBridge : Solve
            FrontendLLBBridge ->> Job : Build
            activate Job
            note over FrontendLLBBridge, Frontend : Implementations may also<br/>call FrontendLLBBridge to<br/>solve graphs before<br/>returning the result.
            Job -->> FrontendLLBBridge : Result
            deactivate Job
            FrontendLLBBridge -->> Frontend : Result
        end

        Frontend -->> FrontendLLBBridge : Result
        deactivate Frontend
    end

    FrontendLLBBridge -->> Solver : Result
    Solver ->> FrontendLLBBridge : Discard
    deactivate FrontendLLBBridge

    Solver ->> Job : Discard
    deactivate Job

    Solver -->> ControlServer : Result
    ControlServer -->> ControlClient : Result
```

An important detail is that frontends may also issue solve requests, which are
often definition-based solves, but can also be frontend-based solves, allowing
for composability of frontends. Note that if a frontend makes a frontend-based
solve request, they will share the same FrontendLLBBridge and underlying job.

## Dockerfile frontend (`dockerfile.v0`)

Buildkit comes with a Dockerfile frontend which essentially is a parser that
translates Dockerfile instructions into a LLB definition. In order to introduce
new features into the Dockerfile DSL without breaking backwards compatibility,
Dockerfiles can include a syntax directive at the top of the file to indicate a
frontend image to use.

For example, users can include a syntax directive to use
`docker/dockerfile:1-labs` to opt-in for an extended Dockerfile DSL that
takes advantage of Buildkit features. However, the frontend image doesn’t have
to be Dockerfile-specific. One can write a frontend that reads a YAML file, and
using the syntax directive, issue the build request using `docker build -f
my-config.yaml`.

The lifecycle of a `dockerfile.v0` frontend-based solve request goes through
the following:

1. Starting from the "frontend-based solve" path, the bridge looks up the
   Dockerfile frontend if the frontend key is `dockerfile.v0`, and requests a
   solve to the frontend. The gateway forwarder implements the frontend
   interface and wraps over a BuildFunc that builds Dockerfiles.
2. The BuildFunc issues a solve request to read the Dockerfile from a source
   (local context, git, or HTTP), and parses it to find a syntax directive.
   - If a syntax directive is found, it delegates the solve to the `gateway.v0`
     frontend.
   - If a syntax directive is not found, then it parses the Dockerfile
     instructions and builds an LLB. The LLB is marshaled into a definition and
     sent in a solve request.

<!-- Diagram from https://gist.github.com/hinshun/2c18e16b07b38049bac72a4b602985b5 -->
```mermaid
sequenceDiagram
    participant Job
    participant FrontendLLBBridge

    # FIXME: use boxes with https://github.com/mermaid-js/mermaid/issues/1505
    # box "Dockerfile frontend"
    participant Frontend as Gateway Forwarder
    participant BuildFunc
    # end box

    # FIXME: use incoming messages with https://github.com/mermaid-js/mermaid/issues/1357
    Job ->> FrontendLLBBridge : Solve
    FrontendLLBBridge ->> Frontend : Solve

    Frontend ->> BuildFunc : Call
    activate BuildFunc

    BuildFunc ->> FrontendLLBBridge : Solve
    FrontendLLBBridge ->> Job : Build
    activate Job
    note over Frontend : Solve to read<br/>Dockerfile
    Job -->> FrontendLLBBridge : Result
    deactivate Job
    FrontendLLBBridge -->> BuildFunc : Result

    alt Dockerfile has syntax directive
        BuildFunc ->> FrontendLLBBridge : Solve
        activate FrontendLLBBridge #FFBBBB
        note over Frontend : Dockerfile delegates<br/>to gateway.v0
        FrontendLLBBridge -->> BuildFunc : Result
        deactivate FrontendLLBBridge
    else Dockerfile has no syntax directive
        BuildFunc ->> FrontendLLBBridge : Solve
        FrontendLLBBridge ->> Job : Build
        activate Job
        note over Frontend : Solved by<br/>Dockerfile2LLB
        Job -->> FrontendLLBBridge : Result
        deactivate Job
        FrontendLLBBridge -->> BuildFunc : Result
    end

    BuildFunc -->> Frontend : Return
    deactivate BuildFunc

    Frontend -->> FrontendLLBBridge : Result
    FrontendLLBBridge -->> Job : Result
```

## Gateway frontend (`gateway.v0`)

The gateway frontend allows external frontends to be implemented as container
images, allowing for a pluggable architecture. The container images have access
to the gRPC service through stdin/stdout. The easiest way to implement a
frontend image is to create a golang binary that vendors buildkit because they
have a convenient LLB builders and utilities.

The lifecycle of a `gateway.v0` frontend-based solve request goes through the
following:

1. Starting from the "frontend-based solve" path, the bridge looks up the
   Gateway frontend if the frontend key is `gateway.v0`, and requests a solve
   to the frontend.
2. The gateway frontend resolves a frontend image from the `source` key
   and solves the request to retrieve the rootfs for the image.
3. A temporary gRPC server is created that forwards requests to the LLB bridge.
4. A container using the frontend image rootfs is created, and a gRPC
   connection is established from a process inside the container to the
   temporary bridge forwarder.
5. The frontend image is then able to build LLBs and send solve requests
   through the forwarder.
6. The container exits, and then the results are plumbed back to the LLB
   bridge, which plumbs them back to the client.

<!-- Diagram from https://gist.github.com/hinshun/ecf554c32522fc94a33488b353230b27 -->
```mermaid
sequenceDiagram
    participant Job
    participant FrontendLLBBridge
    participant Frontend as Gateway frontend
    participant Worker
    participant LLBBridgeForwarder
    participant Executor
    participant Container as Frontend Container
    
    Job ->> FrontendLLBBridge : Solve
    FrontendLLBBridge ->> Frontend : Solve
    Frontend ->> Worker : ResolveImageConfig
    activate Worker
    Worker -->> Frontend : Digest
    deactivate Worker
    Frontend ->> FrontendLLBBridge : Solve
    
    FrontendLLBBridge ->> Job : Build
    activate Job
    note over FrontendLLBBridge, Frontend : The frontend image specified<br/>by build option "source" is solved<br/>and the rootfs of that image<br/>is then used to run the container.
    Job -->> FrontendLLBBridge : Result
    deactivate Job
    
    FrontendLLBBridge -->> Frontend : Result
    
    note over LLBBridgeForwarder, Executor : A temporary gRPC server is created <br/>that listens on stdio of frontend<br/>container. Requests are then<br/>forwarded to LLB bridge.
    Frontend ->> LLBBridgeForwarder : Create forwarder
    activate LLBBridgeForwarder
    
    Frontend ->> FrontendLLBBridge : Exec
    FrontendLLBBridge ->> Worker : Exec
    Worker ->> Executor : Exec
    
    Executor ->> Container : Create container task
    activate Container #MediumSlateBlue
    
    rect rgba(100, 100, 100, .1)
      note over Executor, Container : Frontend images may request<br/>definition/frontend-based solves<br/>like any other client.
      loop
        Container ->> LLBBridgeForwarder : Solve
        LLBBridgeForwarder ->> FrontendLLBBridge : Solve
        activate FrontendLLBBridge #FFBBBB
        FrontendLLBBridge -->> LLBBridgeForwarder : Result
        deactivate FrontendLLBBridge
        LLBBridgeForwarder -->> Container : Result
      end
    end
    
    Container -->> Executor : Exit
    deactivate Container
    
    Executor -->> Worker : Exit
    Worker -->> FrontendLLBBridge : Exit
    FrontendLLBBridge -->> Frontend : Exit
    Frontend ->> LLBBridgeForwarder : Discard
    deactivate LLBBridgeForwarder
    
    Frontend -->> FrontendLLBBridge : Result
    FrontendLLBBridge -->> Job : Result
```
