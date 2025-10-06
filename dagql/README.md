# dagql

DagQL is a strongly opinionated implementation of a GraphQL server.

## axioms

Below are a set of assertions that build on one another.

* All Objects are immutable.
* All Objects are [Nodes][Node], i.e. all objects have an `id`.
* All Objects have their own ID type, e.g. `PointID`.
* All Objects have a top-level constructor named after the object, e.g. `point`.
* All Objects may be loaded from an ID, which will create the Object if needed.
* An Object's field may be `@impure` which indicates that the field's result shall not be cached.
* An Object's field may be `@meta` which indicates that the field may be omitted without affecting the result.
* All IDs are derived from the query that constructed the Object.
* An ID is *canonicalized* by removing any embedded `@meta` selectors.
* An ID is *impure* if it contains any `@impure` selectors or any *tainted* IDs.
* An ID may be loaded on a server that has never seen its Object before.
* When a *pure* ID is loaded it must always return the same Object.
* When an *impure* ID is loaded it may return a different Object each time.
* An *impure* query or ID may return an Object with a *pure* ID.
* All data may be kept in-memory with LRU-like caching semantics.
* All Arrays returned by Objects have deterministic order.
* An ID may refer to an Object returned in an Array by specifying the *nth* index (starting at 1).
* All Objects in Arrays have IDs: either an ID of their own, or the field's ID with *nth* set.
* At the GraphQL API layer, Objects are passed to each other by ID.
* At the code layer, Objects received as arguments are automatically loaded from a given ID.

[Node]: https://graphql.org/learn/global-object-identification/

## context

This repository might be re-integrated into Dagger, but for now is just a
personal experiment.

It should replace our use of the following forks:

* `github.com/dagger/graphql`
* `github.com/dagger/graphql-go-tools`

I think it may make sense to leave as its own repo just to make sure there's a
clear boundary between the theory and the practice. But it should probably move
into the Dagger account.

## TODO

* [x] parallel query execution
* [x] figure out whether constructor patterns are enshrined or ad-hoc
    * ad-hoc; you can now Extend any ObjectType in the Server.
* [x] figure out how to return objects that already have an ID (e.g.
  `loadFooFromID` should not have itself in the returned ID)
    * if resolvers return an Object no further wrapping/ID-ing is required
* [x] implement caching semantics, including `@impure` and `@meta`
* [x] figure out telemetry
    * resolvers can be wrapped, have access to the current `*idproto.ID`
* [x] support schema docs for everything (types, fields, args, enum values, etc)
* [ ] figure out how interfaces work
    * don't need this yet, but might be a next step for Dagger interfaces
* [x] IDs should also contain module info
    * IDs can now contain a reference to a single module ID which provides the
      implementation of the field that the ID resolves
    * all modules contained recursively by an ID (i.e. in parents or arguments)
      are loaded prior to evaluating the ID
      * technically we might not need to load _all_ modules, since arguably
        arguments could be evaluated in a vacuum; TBD
* [ ] IDs should also contain digest of result (stretch goal, this is higher
  level, e.g. we want literal file checksums for objects that represent a file)
* [x] get rid of Identified in favor of Object? (see interfaces + wrapping concern below)
