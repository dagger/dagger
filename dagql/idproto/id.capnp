@0x93d909241605aeda;

using Go = import "/go.capnp";
$Go.package("idproto");
$Go.import("github.com/dagger/dagger/core/idproto");

struct ID {
  # ID represents a GraphQL object of a certain type, constructed by evaluating
  # its contained pipeline. In other words, it represents a
  # constructor-addressed object.
  #
  # It may be binary=>base64-encoded to be used as a GraphQL ID value.
  # Alternatively it may be stored in a database and referred to via an RFC-6920
  # ni:#sha-256;... URI.

  type @@ :Type;
  # GraphQL __typename of the object.

  constructor @1 :List(Selector);
  # Chain of GraphQL field selections that construct the object, starting from
  # Query.
}

struct Selector {
  # Selector is an individual field and arguments. Its result will either be
  # passed to the next Selector in the pipeline or returned as the final ID
  # result.
  #
  # I can't explain why, but this name feels more satisfying than the drier
  # alternatives ("field", "call", ...). Feel free to call me out and suggest
  # alternatives.

  field @0 :Text;
  # GraphQL field name.

  args @1 :List(Argument);
  # GraphQL field arguments, always in alphabetical order.

  tainted @2 :Bool;
  # If true, this Selector is not reproducible.
  #
  # TODO: do we need to refer to session/client IDs or anything here? Or is
  # that all internal? Forcing function is whether this is used as an
  # in-memory query cache key. But the query cache might be made per-session
  # or even per-client instead anyway! What buys us the most?

  meta @3 :Bool;
  # If true, this Selector may be omitted from the pipeline without changing
  # the ultimate result.
  #
  # This is used to prevent meta-queries like 'pipeline' and 'withFocus' from
  # busting cache keys when desired.
  #
  # It is worth noting that we don't store meta information at this level and
  # continue to force metadata to be set via GraphQL queries. It makes IDs
  # always easy to evaluate.

  nth @3 :Int32;
  # If the field returns a list, this is the index of the element to select.
  # Note that this defaults to zero, as IDs always refer to
  #
  # Here we're teetering dangerously close to full blown attribute path
  # selection, but we're intentionally limiting ourselves instead to cover only
  # the common case of returning a list of objects. The only case not handled
  # is a nested list. Don't do that; have a type instead.
}

struct Argument {
  # A named value passed to a GraphQL field or contained in an input object.

  name @0 :Text;
  value @1 :Literal;
}

struct Literal {
  # A value passed to an argument or contained in a list.

  union {
    id @0 :ID;
    null @1 :Void;
    bool @2 :Bool;
    enum @3 :Text;
    int @4 :Int64;
    float @5 :Float64;
    string @6 :Text;
    list @7 :List(Literal);
    object @8 :List(Argument);
  }
}

struct Type {
  union {
    named @@ :Text;
    list @0 :Type;
  }
  nonNull @1 :Bool;
}
