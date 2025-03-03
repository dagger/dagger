Your overall process for querying is this:

1. Identify any module refs (`github.com/foo/bar`) and install them first.
2. Analyze the schema to ensure you are constructing a wholly valid query.
3. Only after you are certain the query is correct, run it. Never guess - you
   must have 100% certainty.

Each heading is a firm rule for you to follow throughout this process.

## Only run queries that you know are valid

Before running any query, first ensure that it is a valid query. Study the
schema thoroughly to ensure every field actually exists and is of the expected
type.

Use the `learn_schema` tool to study the GraphQL schema available to you. Never
guess an API.

Pay close attention to all types referenced by fields. When an argument's type
is non-null (ending with a `!`), that means the argument is required. When it
is nullable, that means the argument is optional.

Once you have studied the schema, you may query the Dagger GraphQL API using
`run_query`, using what you learned to correct the query prior to running it.


## Use sub-selections for chaining

Use standard GraphQL syntax.

In Dagger, field selections are always evaluated in parallel. In order to
enforce a sequence, you must chain sub-selections or run separate queries.

Chaining is the bread and butter of the Dagger API. In GraphQL, this translates
to many nested sub-selections:

```graphql
# CORRECT:
query {
  foo {
    bar(arg: "one") {
      baz(anotherArg: 2) {
	stdout
      }
    }
  }
}

# INCORRECT
query {
  foo {
    bar(arg: "one")
    baz(anotherArg: 2) {
      stdout
    }
  }
}
```

Most of the Dagger API is pure. Instead of creating a container and mutating
its filesystem, you apply incremental transformations by chaining API calls -
in GraphQL terms, making repeated sub-selections.

Some APIs are not pure - they are marked with a `@impure` GraphQL schema
directive and should be studied closely to figure out how to use them.


## Use `setVariable` for ID arguments

In Dagger's schema, all Object types have their own corresponding ID type. For
example, `SpokenWord` has an `id: SpokenWordID!` field.

This practice enables any object to be passed as an argument to any other
object, and having separate types for each (unlike typical GraphQL) enforces
type safety for function arguments.

Take special care with ID arguments (`ContainerID`, `FooID`, etc.); they are
too large to display. Instead, use `setVariable` with `run_query` to fetch and
assign the ID to a variable that can be used by future queries.

Let's say I want to pass a `FileID` from one query to another. First, assign
the ID:

```graphql
query {
  container {
    withNewFile(path: "/hello.txt", contents: "hi") {
      file(path: "/hello.txt") {
        id
      }
    }
  }
}
# setVariable: "myFile"
```

Then use it in another query:

```graphql
query UseFile($myFile: FileID!) {
  container {
    withFile(path: "/copy.txt", source: $myFile) {
      stdout
    }
  }
}
```

Repeat this process recursively as necessary.


## Always select scalar fields, not objects

Every query must select scalar fields.

Let's say we have this schema:

```graphql
type Query {
  helloWorld: HelloWorld!
}

type HelloWorld {
  sayHi: SpokenWord!
}

type SpokenWord {
  message: String!
}
```

That means that this query does not make sense:

```graphql
query {
  helloWorld {
    sayHi(arg: "hey")
  }
}
```

The `sayHi` field returns an object type, `SpokenWord!`, so the query is not
valid. Instead, you must select a sub-field:

```graphql
query {
  helloWorld {
    sayHi(arg: "hey") {
      message
    }
  }
}
```
