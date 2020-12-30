# Prototype 69-dagger-archon

This is an improved version of 64-blagger with the cuellb pattern, using
everything we learned in 64,64,66.

This prototype marks a strong convergence between SH and AL on the high-level DX and architecture
of the future OSS project. The next step is to get to a working POC implementation together,
that can run a configuration written by SA, end-to-end, and make SA happy with the DX :)

## Points of convergence:

### 1. Cue DX: the cuellb pattern

The "cuellb" pattern is a promising DX for writing delivery workflows in Cue.
On the one hand, the raw arrays are verbose, but they are easy to understand. And they
can be abstracted away by the community as they wish, using the native features of Cue.
TO quote AL: "It's easier to take something verbose and simple, and make it less verbose,
than to take something concise and complex, and make it simple."

### 2. Integration of SAAS features: runtime+engine

The OSS project must combine features from 3 codebases: `bl-runtime`, `bl-api`, and `bl`.
And it must combine them in a way that allows maximum code sharing between SAAS and OSS,
so we don't have to do everything twice.

So in addition to defining a Cue DX and runtime, we also need to define how `dagger` will
implement (or avoid implementing) saas features like settings, secrets, connectors, stacks,
envs, slugs, job history, provider catalog, etc.

A  promising approach to do this is to split the OSS project in 2 components:

- 1) the runtime which loads and executes cue confiurations, and
- 2) the engine which orchestrates the runtime, and all its inputs and outputs: config repositories,
settings, dependencies, previous state, execution history, etc.
The engine is also responsible for end-user interaction.
(see 64-blagger/README.md for more details on this architecture).

### 3. Possible performance gains

One consequence of the cuellb model + engine/runtime split, is that it becomes possible (we think)
to compile a dagger job to a single LLB slug, and run it once.
This has several benefits, including making the runtime simpler, and removing the hard dependency
on a registry (which opens the door to new use cases in local development).

One possible benefit is performance. In theory, single-pass llb compilation is automatically faster
because it removes the multiple round-trips between 1) cue eval 2) buildkit run 3) registry push 4) registry pull.
The larger the configuration, the more round-trips, and the bigger the potential performance gain in dagger.

*[SH]* I have done lots of research work on this (prototypes 64-68) and the results are very encouraiging.
I have strong conviction that the performance benefits are huge. But I only have bits and pieces of POC implementations,
each focusing on a small aspect of the problem. The next step is to implement an end-to-end POC, and run a crude benchmark.


### 4. Next step: working end-to-end prototype

We agree on a possible high-level architecture, DX and UX, built on several hypothesis:
is cuellb really a good DX in practice? can dir refs really be eliminated?
is single-pass llb jit really feasible, and if so does it solve our performance issues?
how easy will it be to port Blocklayer to dagger? How much work to migrate beta users?

Now is the time to test these hypothesis with a working end-to-end implementation.
There are many remaining questions but we have enough alignment to find the answers
together.
