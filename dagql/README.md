# dagql

DagQL is a strongly opinionated implementation of a GraphQL server.

## axioms

Below are a set of assertions that build on one another.

* All Objects are immutable.
* All Objects are [Node]s, i.e. all objects have an `id`.
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
* An ID may refer to an Object returned in an Array by specifing the *nth* index (starting at 1).
* All Objects in Arrays have IDs: either an ID of their own, or the field's ID with *nth* set.

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
