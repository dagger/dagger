# Buildkit solver design

The solver is a component in BuildKit responsible for parsing the build
definition and scheduling the operations to the workers for execution.

Solver package is heavily optimized for deduplication of work, concurrent
requests, remote and local caching and different per-vertex caching modes. It
also allows operations and frontends to call back to itself with new definition
that they have generated.

The implementation of the solver is quite complicated, mostly because it is
supposed to be performant with snapshot-based storage layer and distribution
model using layer tarballs. It is expected that calculating the content based
checksum of snapshots between every operation or after every command execution
is too slow for common use cases and needs to be postponed to when it is likely
to have a meaningful impact. Ideally, the user shouldn't realize that these
optimizations are taking place and just get intuitive caching. It is also hoped
that if some implementations can provide better cache capabilities, the solver
would take advantage of that without requiring significant modification.

In addition to avoiding content checksum scanning the implementation is also
designed to make decisions with minimum available data. For example, for remote
caching sources to be effective the solver will not require the cache to be
loaded or exists for all the vertexes in the graph but will only load it for
the final node that is determined to match cache. As another example, if one of
the inputs (for example image) can produce a definition based cache match for a
vertex, and another (for example local source files) can only produce a
content-based(slower) cache match, the solver is designed to detect it and skip
content-based check for the first input(that would cause a pull to happen).

## Build definition

The solver takes in a build definition in the form of a content addressable
operation definition that forms a graph.

A vertex in this graph is defined by these properties:

```go
type Vertex interface {
    Digest() digest.Digest
    Options() VertexOptions
    Sys() interface{}
    Inputs() []Edge
    Name() string
}

type Edge struct {
    Index Index
    Vertex Vertex
}

type Index int
```

Every vertex has a content-addressable digest that represents a checksum of the
definition graph up to that vertex including all of its inputs. If two vertexes
have the same checksum, they are considered identical when they are executing
concurrently. That means that if two other vertexes request a vertex with the
same digest as an input, they will wait for the same operation to finish.

The vertex digest can only be used for comparison while the solver is running
and not between different invocations. For example, if parallel builds require
using `docker.io/library/alpine:latest` image as one of the operations, it is
pulled only once. But if a build using `docker.io/library/alpine:latest` was
built earlier, the checksum based on that name can't be used for finding if the
vertex was already built because the image might have changed in the registry
and "latest" tag might be pointing to another image.

`Sys()` method returns an object that is used to resolve the executor for the
operation. This is how a definition can pass logic to the worker that will
execute the task associated with the vertex, without the solver needing to know
anything about the implementation. When the solver needs to execute a vertex,
it will send this object to a worker, so the worker needs to be configured to
understand the object returned by `Sys()`. The solver itself doesn't care how
the operations are implemented and therefore doesn't define a type for this
value. In LLB solver this value would be with type `llb.Op`.

`Inputs()` returns an array of other vertexes the current vertex depends on. A
vertex may have zero inputs. After an operation has executed, it returns an
array of return references. If another operation wants to depend on any of
these references they would define an input with that vertex and an index of
the reference from the return array(starting from zero). Inputs need to be
contained in the `Digest()` of the vertex - two vertexes with different inputs
should never have the same digest.

Options contain extra information that can be associated with the vertex but
what doesn't change the definition(or equality check) of it. Normally this is
either a hint to the solver, for example, to ignore cache when executing. It
can also be used for associating messages with the vertex that can be helpful
for tracing purposes.

## Operation interface

Operation interface is how the solver can evaluate the properties of the actual
vertex operation. These methods run on the worker, and their implementation is
determined by the value of `vertex.Sys()`. The solver is configured with a
"resolve" function that can convert a `vertex.Sys()` into an `Op`.

```go
// Op is an implementation for running a vertex
type Op interface {
    // CacheMap returns structure describing how the operation is cached.
    // Currently only roots are allowed to return multiple cache maps per op.
    CacheMap(context.Context, int) (*CacheMap, bool, error)
    // Exec runs an operation given results from previous operations.
    // Note that this is not the process execution but can have any definition.
    Exec(ctx context.Context, inputs []Result) (outputs []Result, err error)
}

type CacheMap struct {
    // Digest is a base digest for operation that needs to be combined with
    // inputs cache or selectors for dependencies.
    Digest digest.Digest
    Deps   []struct {
        // Optional digest that is merged with the cache key of the input
        Selector digest.Digest
        // Optional function that returns a digest for the input based on its
        // return value
        ComputeDigestFunc ResultBasedCacheFunc
    }
}

type ResultBasedCacheFunc func(context.Context, Result) (digest.Digest, error)


// Result is an abstract return value for a solve
type Result interface {
    ID() string
    Release(context.Context) error
    Sys() interface{}
}
```

There are two functions that every operation defines. One describes how to
calculate a cache key for a vertex and another how to execute it.

`CacheMap` is a description for calculating the cache key. It contains a digest
that is combined with the cache keys of the inputs to determine the stable
checksum that can be used to cache the operation result. For the vertexes that
don't have inputs (roots), it is important that this digest is a stable secure
checksum. For example, in LLB this digest is a manifest digest for container
images or a commit SHA for git sources.

`CacheMap` may also define optional selectors or content-based cache functions
for its inputs.

- A selector is combined with the cache key of an input, to create a modified
  version of that key. In LLB this is used for describing when different
  files of an input snapshot are being used.
- A content-based cache function allows computing a new cache key for an input
  after it has completed. In LLB this is used for calculating a cache key based
  on the checksum of file contents of the input snapshots.
  
> **Note**
>
> For example, in the case of LLB, if a vertex is a FileOp that copies a file
> from one snapshot to another, the selector can be set to the path of the
> source file in the input snapshot, while the content-based cache function can
> be used to calculate the checksum of the file contents.
> 
> If the source path changes, we need to invalidate the cache (which we do by
> changing the selector). However, if we do a content-based cache lookup for
> the input, then the file content may not have changed (which we can detect by
> hashing the file contents). In this case, we can reuse the cache result even
> when the source path has changed.
>
> This abstraction allows the [scheduler](#scheduler) to determine whether to
> perform a quick selector-based cache lookup or a slower content-based cache
> lookup.

`Exec` executes the operation defined by a vertex by passing in the results of
the inputs.

## Shared graph

After new build request is sent to the solver, it first loads all the vertexes
to the shared graph structure. For status tracking, a job instance needs to be
created, and vertexes are loaded through jobs. A job ID is assigned to every
vertex. If vertex with the same digest has already been loaded to the shared
graph, a new job ID is appended to the existing record. When the job finishes,
it removes all of its references from the loaded vertex. The resources are
released if no more references remain.

Loading a vertex also creates a progress writer associated with it and sets up
the cache sources associated with the specific vertex.

After vertexes have been loaded to the job, it is safe to request a result from
an edge pointing to a previously loaded vertex. To do this `build(ctx, Edge)
(CachedResult, error)` method is called on the static scheduler instance
associated with the solver.

## Scheduler

The scheduler is a component responsible for invoking the individual operations
needed to find the result for the graph. While the build definition is defined
with vertexes, the scheduler is solving edges. In the case of LLB solver, a
result of a solved edge is associated with a snapshot. Usually, to solve an
edge, the input edges need to be solved first and this can be done
concurrently, but there are many exceptions like edge may be cached but its
input might be not, or solving one input might cause a cache hit while solving
others would just be wasteful. Scheduler tries do handle all these cases.

The scheduler is implemented as a single threaded non-blocking event loop. The
single threaded constraint is for simplicity and might be removed in the future -
currently, it is not known if this would have any performance impact. All the
events in the scheduler have one fixed sender and receiver. The interface for
interacting with the scheduler is to create a "pipe" between a sender and a
receiver. One or both sides of the pipe may be an edge instance of the graph.
If a pipe is added it to the scheduler and an edge receives an event from the
pipe, the scheduler will "unpark" that edge so it can process all the events it
had received.

The unpark handler for an edge needs to be non-blocking and execute quickly.
The edge will process the data from the incoming events and update its internal
state. When calling unpark, the scheduler has already separated out the sender
and receiver sides of the pipes that in the code are referred as incoming and
outgoing requests. The incoming requests are usually requests to retrieve a
result or a cache key from an edge. If it appears that an edge doesn't have
enough internal state to satisfy the requests, it can make new pipes and
register them with the scheduler. These new pipes are generally of two types:
ones asking for some async function to be completed and others that request an
input edge to reach a specific state first.

To avoid bugs and deadlocks in this logic, the unpark method needs to follow
the following rules. If unpark has finished without completing all incoming
requests it needs to create outgoing requests. Similarly, if an incoming
request remains pending, at least one outgoing request needs to exist as well.
Failing to comply with this rule will cause the scheduler to panic as a
precaution to avoid leaks and hiding errors.

## Edge state

During unpark, edge state is incremented until it can fulfill the incoming
requests.

An edge can be in the following states: initial, cache-fast, cache-slow,
completed. Completed edge contains a reference to the final result,
in-progress edge may have zero or more cache keys.

The initial state is the starting state for any edge. If a state has reached a
cache-fast state, it means that all the definition based cache key lookups have
been performed. Cache-slow means that content-based cache lookup has been
performed as well. If possible, the scheduler will avoid looking up the slow
keys of inputs if they are unnecessary for solving current edge.

The unpark method is split into four phases. The first phase processes all
incoming events (responses from outgoing requests or new incoming requests)
that caused the unpark to be called. These contain responses from async
functions like calls to get the cachemap, execution result or content-based
checksum for an input, or responses from input edges when their state or number
of cache keys has changed. All the results are stored in edge's internal state.
For the new cache keys, a query is performed to determine if any of them can
create potential matches to the current edge.

After that, if any of the updates caused changes to edge's properties, a new
state is calculated for the current vertex. In this step, all potential cache
keys from inputs can cause new cache keys for the edge to be created and the
status of an edge might be updated.

Third, the edge will go over all of its incoming requests, to determine if the
current internal state is sufficient for satisfying them all. There are a
couple of possibilities how this check may end up. If all requests can be
completed and there are no outgoing requests the requests finish and unpark
method returns. If there are outgoing requests but the edge has reached the
completed state or all incoming requests have been canceled, the outgoing
requests are canceled. This is an async operation as well and will cause unpark
to be called again after completion. If this condition didn't apply but
requests could be completed and there are outgoing requests, then the incoming
request is answered but not completed. The receiver can then decide to cancel
this request if needed. If no new data has appeared to answer the incoming
requests, the desired state for an edge is determined for an edge from the
incoming requests, and we continue to the next step.

The fourth step sets up outgoing requests based on the desired state determined
in the third step. If the current state requires calling any async functions to
move forward then it is done here. We will also loop through all the inputs to
determine if it is important to raise their desired state. Depending on what
inputs can produce content based cache keys and what inputs have already
returned possible cache matches, the desired state for inputs may be raised at
different times.

When an edge needs to resolve an operation to call the async `CacheMap` and
`Exec` methods, it does so by calling back to the shared graph. This makes sure
that two different edges pointing to the same vertex do not execute twice. The
result values for the operation that is shared by the edges is also cached
until the vertex is cleaned up. Progress reporting is also handled and
forwarded to the job through this shared vertex instance.

Edge state is cleaned up when a final job that loaded the vertexes that they
are connected to is discarded.

## Cache providers

Cache providers determine if there is a result that matches the cache keys
generated during the build that could be reused instead of fully reevaluating
the vertex and its inputs. There can be multiple cache providers, and specific
providers can be defined per vertex using the vertex options.

There are multiple backend implementations for cache providers, in-memory one
used in unit tests, the default local one using bbolt and one based on cache
manifests in a remote registry.

Simplified cache provider has following methods:

```go
Query(...) ([]*CacheKey, error)
Records(ck *CacheKey) ([]*CacheRecord, error)
Load(ctx context.Context, rec *CacheRecord) (Result, error)
Save(key *CacheKey, s Result) (*ExportableCacheKey, error)
```

Query method is used to determine if there exist a possible cache link between
the input and a vertex. It takes parameters provided by `op.CacheMap` and cache
keys returned by the calling the same method on its inputs.

If a cache key has been found, the matching records can be asked for them. A
cache key can have zero or more records. Having a record means that a cached
result can be loaded for a specific vertex. The solver supports partial cache
chains, meaning that not all inputs need to have a cache record to match cache
for a vertex.

Load method is used to load a specific record into a result reference. This
value is the same type as the one returned by the `op.Exec` method.

Save allows adding more records to the cache.

## Merging edges

One final piece of solver logic allows merging two edges into one when they
have both returned the same cache key. In practice, this appears for example
when a build uses image references `alpine:latest` and `alpine@sha256:abcabc`
in its definition and they actually point to the same image. Another case where
this appears is when same source files from different sources are being used as
part of the build.

After scheduler has called `unpark()` on an edge it checks it the method added
any new cache keys to its state. If it did it will check its internal index if
another active edge already exists with the same cache key. If it does it
performs some basic validation, for example checking that the new edge has not
explicitly asked cache to be ignored, and if it passes, merges the states of
two edges.

In the result of the merge, the edge that was checked is deleted, its ongoing
requests are canceled and the incoming ones are added to the original edge.
