---
slug: /2sl2b/design
displayed_sidebar: "0.3"
---

# Design

## Goals

NOTE: this currently focuses more on new goals in cloak (v0.3) relative to europa (v0.2); it does not yet include much about other general Dagger goals that have been retained between versions (like portability).

### API-centric

Functionality in Dagger should be exposed through language-agnostic APIs. This includes both:

- "Core" APIs, which operate as Dagger's "standard library" and provide basic primitives like the ability to pull container images, execute commands, etc.
- Extensions, which are like external libraries that build higher-level tools and abstractions on top of the primitives provided by the core APIs and other extensions.

The API framework Dagger uses should enable:

1. Calling of automation from anywhere and any language
   - The API may be invoked from a CLI command, from a script written in any of our supported languages, from an HTTP request sent by a browser, etc.
1. Authoring of automation in any language
   - The caller of an API doesn't need to know what language the API is implemented with.
   - This freedom leaves both caller and callee to choose whichever language fits their particular job the best without having to think about interoperability.
   - Rather than reimplementing the same automation in every possible programming language, it only needs to be implemented once and s then re-usable by all.
   - Strengths of different languages thus enhance each other rather than creating silos.
     - If Python has the world's greatest library for accomplishing X, an extension author should feel free to import+use it; they will be making that functionality available for anyone else in Go, Typescript, Bash and any other Dagger-supported language along the way.
   - Additionally, two cooperating teams no longer necessarily have to come to consensus on a common language.
     - One team can implement automation in Go and then provide it to another team that uses Javascript.
1. Type-safety
   - The inputs and output of APIs should all have well-defined types+shapes. Languages that have type checking should be able to use this to enable compile-time checks for devs calling and authoring extensions.
1. Low-boilerplate
   - The acts of A) defining an API and B) calling an API should not involve lots of boilerplate or domain-specific knowledge.

Dagger uses GraphQL as its low-level language-agnostic API framework for achieving the above goals. The reasons for this are discussed more in the FAQ below.

### Language-native feeling

The Dagger SDK for each language should feel natural and intuitive to those already familiar with that language. It should embrace the strengths of the language and not fight any of its conventions. You should not need to become an expert in the Dagger SDK to use the Dagger SDK.

There are many aspects to the above, but a few selected implications are:

1. Defining an API should not feel very different to just defining a function in the language you choose to use. The conversion of those functions to language-agnostic API calls should be fully hidden behind the Dagger SDK.
   - We call this feature "code-first schemas"
1. Calling an API should not feel very different than just calling functions in the language you choose to use. The conversion of those function calls to language-agnostic API calls should be fully hidden behind the Dagger SDK.
   - We support this through code-generated SDKs, which implement typed interfaces to extension APIs.

## FAQ

### Do I have to know GraphQL to use Dagger?

The goal is no, you should only need to know one of Dagger's supported languages to use Dagger. GraphQL should be abstracted away through aforementioned code-first schemas and code-generated clients.

The only exceptions to this are:

1. If you like GraphQL and want to use it, feel free. Dagger doesn't actually hide the GraphQL layer or make it inaccessible to callers. We just provide abstractions on top of it for those who prefer a DX closer to their programming language.
1. Particularly advanced use cases. E.g.
   - Some highly tuned queries, such as those that request lots of different data from different branches, may not be expressable with the code-generated client. While need for this is expected to be rare, these cases can fallback to raw graphql queries.
   - Some advanced techniques when defining schemas, such as "chainable" types that enable builder-patterns in queries, may require a basic understanding of the GraphQL execution model to implement.
   - Writing an SDK for a new language

### Why GraphQL?

GraphQL is not normally associated with the "DevOps" space Dagger occupies. However, while experimenting with a few different approaches early on, we tried GraphQL and found it to be a great fit for a number of different reasons.

There are many practical reasons it worked out, but on a very high "philosophical" level:

- Dagger is centered around the idea of DAGs, where the nodes of the DAG represent typed artifacts+actions in a CI/CD pipeline.
- GraphQL meanwhile is a framework for defining typed schemas and executing chains of operations utilizing those types.
- The connection point is that GraphQL schemas can be used to type the nodes of Dagger's DAG and GraphQL operations can then compose different nodes together, which when all combined together results in your full CI/CD DAG.

To break this down a bit more: GraphQL actually serves a couple of different roles:

1. A schema-definition language
   - The GraphQL schema-definition language (SDL) strikes a good balance of simplicity and featurefulness.
   - This allows Dagger SDKs to support most APIs that users want to define while not being so specialized that it becomes hard to abstract over in one programming language or another.
1. An operation-definition language
   - GraphQL queries themselves have a well-defined format that's capable of both simple "calls" (almost like an RPC) but also more advanced queries that involve chains of operations and types.
   - In the context of Dagger, this gives us a language-agnostic format on which typed generated clients can be created in any of our supported languages.
1. An execution framework
   - GraphQL also has a well-defined execution model that makes all of the above surprisingly simple to implement by just writing "resolvers" for fields in the types defined with the schema.

There are many other frameworks which provide a subset of the above benefits and would have been viable implementation choices, but GraphQL checked all the boxes at once.

Additionally, the pre-existing community around GraphQL has created a ton of really slick tooling that we and ours users now get to re-use.

Of course, GraphQL is not absolutely perfect in every way, a few areas where it's a more awkward fit:

1. Lack of built-in ability to submit multiple operations at once where one operation references a result of another.
   - This is more of a low-level limitation though. In higher-level languages you simply split these queries into separate calls and just plug in the result of one to another, the same way you do most of the time when writing code.
   - There are also multiple frameworks popping up in the GraphQL community for addressing this limitation (e.g. a few involving graphql directives), so there's a chance a consensus solution to it may emerge in the future.
1. The difference between queries and mutations has not had an obvious use in the context of Dagger
   - GraphQL has a builtin concept of a mutation, which is different than a query and supposed to be used when changing state (where as queries just retrieve state).
   - Practically speaking, the only difference between mutations and queries is that mutations execute in serial whereas batched queries can run in parallel.
   - For now, Dagger just uses queries as it's not been clear whether mutation make sense in our context.
