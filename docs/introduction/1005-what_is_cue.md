---
sidebar_position: 2
slug: /1005/what-is-cue/
sidebar_label: What is Cue?
---

# What is Cue?

CUE is a powerful configuration language created by Marcel van Lohuizen who co-created the Borg Configuration Language (BCL)—the [language used to deploy all applications at Google](https://storage.googleapis.com/pub-tools-public-publication-data/pdf/43438.pdf). It is a superset of JSON, with additional features to make declarative, data-driven programming as pleasant and productive as regular imperative programming.

## The Need for a Configuration Language

For decades, developers, engineers, and system administrators alike have used some combination of `INI`, `ENV`, `YAML`, `XML`, and `JSON` (as well as custom formats such as for Apache, Nginx, etc) to describe configurations, resources, operations, variables, parameters, state, etc. While these technically work, they are merely _data formats_ and not languages. 

As a _data format_ they each lack the ability to express logic and operate on data directly. Simple&mdash;yet powerful!&mdash;things like if statements, for loops, comprehensions, and string interpolation, among others are just not possible in these languages _directly_—that is to say not without the use of a secondary process. The result is that variables or parameters must be injected, and any logic handled by a templating language (such as Jinja) or a DSL (Domain-specific Language). Often templating languages and DSLs are used in conjuction.

Again, while this technically works, the results are that we end up with code bases, or even single files, that intersperse templating languages with various DSLs, thereby making the code difficult to maintain, challenging to reason about, brittle, and perhaps worst of all _prone to side effects_.

A configuration _language_ such as CUE, allows us to both specify data, as well as _act_ on that data with any logic necessary to achieve the desired output. Furthermore, and perhaps most importantly, CUE allows us to not only specify data as concrete values, but also the _types_ those concrete values can or must be. It gives us the ability to define a _schema_ but unlike doing so with say JSON Schema, CUE can both _define_ and _enforce_ the schema, whereas JSON Schema is merely a definition that requires some other process to enforce it.

For a deeper dive on the problems with configuration as code, check out [The Configuration Complexity Curse](https://blog.cedriccharly.com/post/20191109-the-configuration-complexity-curse/) and [How Cue Wins](https://blog.cedriccharly.com/post/20210523-how-cue-wins/).

## Understanding Cue

We won't attempt to rewrite the [CUE documentation](https://cuelang.org/docs/) or replicate some excellent [CUE tutorials](https://cuetorials.com/), but instead give you enough understanding of CUE to move through the dagger tutorials.

At least for our purposes here you need to understand a few key concepts, each of which will be explained in more detail below:
1. Superset of JSON
2. Types _are_ values
3. Concrete values, no overrides
5. Definitions and schemas
6. Disjunctions, Conjunctions, and Unification

It would be most helpful to [install CUE](https://github.com/cue-lang/cue#download-and-install), but if you prefer you can also try these examples in the [CUE playground](https://cuelang.org/play/).

### A Superset of JSON

What you can express in JSON, you can express in CUE, but not everything in CUE can be expressed in JSON. CUE also supports a "lite" version of JSON where certain characters can be eliminated entirely. Take a look at the following code:

```cue
{
  "Person": {
    "Name": "Bob Smith"
    "Age": 42
  }
}

Person: Name: "Bob Smith"
```

In this example we see that in CUE we have declared a top-level key _Person_ twice: once in a more verbose JSON style with brackets and quotes, and again with a "lite" style without the extra characters; notice also that CUE supports _short hand_: when you are defining a single key within an object, you don't need the curly braces, you can write it as a colon-separated path. [Try it in the CUE playground](https://cuelang.org/play/?id=wOVTT8MmvKx#cue@export@yaml), and notice the output (you can choose different formats). Person is declared twice, but is only output once because CUE is automatically _unifying_ the two declarations of Person. It&rsquo;s ok to declare the same field multiple times, _so long as we provide the same value_. See [Concrete Values, No Overrides](#concreteness-no-overrides) below.

### Types _are_ Values

In the previous example we defined the Person&rsquo;s name as the string literal _"Bob Smith"_ which is a _concrete value_ (more on this later). But nothing so far _enforces_ a type. Nothing prevents us from setting the `Name` field to an `int`, but if this data were to be fed to an API that expects a `string` we'd have an error. Let's define some types:

```cue
Person: {
  Name: string
  Age: int
}

Person: {
  Name: "Bob Smith"
  Age: 42
}

```

Here we&rsquo;ve defined the Person&rsquo;s `Name` field as a `string` and the `Age` field as an `int`. CUE will enforce these types, so any attempt to provide say an integer for the Name or a string for the Age will result in an error. It&rsquo;s worth noting here that the output from this example is the result of _implicit uinification_; we&rsquo;ll talk about _explicit unification_ later. [Try it in the CUE playground](https://cuelang.org/play/?id=p12E8vjFTsc#cue@export@yaml).

### Concrete Values, No Overrides

CUE is ultimately used to export data, and is most useful when that data has been validated against a strong, well-defined schema. In order for CUE to export anything, we must provide _concrete values_ for all defined fields not marked as optional.

In the previous examples we have provided concrete values: "Bob Smith" as a string, and 42 as an int. Were we to leave a required field simply defined as a type without a concrete value, CUE will return an error. Try removing a concrete value from the previous example and note the error you get. CUE will complain of an _incomplete value_.

CUE also prevents you from specifying conflicting values. In other words, CUE does not allow one declaration to override any other. The following example would result in an error:

```cue

Person: {
  Name: "Bob Smith"
  Age: 42
}

Person: {
  Name: "Bob Something Else"
  Age: 42
}
```

### Definitions and Schemas

In a real-world scenario we&rsquo;d likely need to define more than one person, and ensure that each one satisfies the schema. That's where `Definitions` come in handy.

```cue
#Person: {
  Name: string
  Age: int
  Nickname?: string
}

Bob: #Person & {
  Name: "Bob Smith"
  Age: 42
}
```

In this example we&rsquo;ve declared that `#Person` is a _definition_, as denoted by the `#` sign. By doing so we have _constrained_ the Person object to a specific set of fields, each a specific type. Definitions by default are _closed_ meaning that a `#Person` cannot contain any fields not specified in the definition. You will also notice that `Nickname?` contains a `?` which denotes that this field is _optional_. 

Definitions themselves are _not_ exported to final output. To get concrete output, we&rsquo;ve declared that the field `Bob` _is_ a `#Person`, and using the single `&` we _unified_ the `#Person` definition with an object whose _concrete values satisfy the constraints_. 