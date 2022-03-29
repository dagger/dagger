---
slug: /1215/what-is-cue
displayed_sidebar: europa
---

# CUE

## What is CUE?

CUE is a configuration language created by Marcel van Lohuizen, the co-creator of the Borg Configuration Language (BCL), [the language used to deploy all applications at Google](https://storage.googleapis.com/pub-tools-public-publication-data/pdf/43438.pdf).

CUE is the result of years of experience writing configuration languages at Google, and seeks to improve the developer experience while avoiding certain pitfalls.
CUE is a superset of JSON with additional features to make declarative, data-driven programming pleasant and productive.

## Why a configuration language?

A _configuration language_ such as CUE, allows us to both _specify_ data as well as _act_ upon that data with any logic necessary to achieve the desired output.

CUE allows us to not only specify data as concrete values, but also specify the _types_ those concrete values must be as well as any _constraints_ such as min and max for example.
It gives us the ability to define a _schema_ but unlike e.g. JSON Schema, CUE can both _define_ and _enforce_ the schema.
In contrast, JSON Schema is merely a definition that requires some other process to enforce it.

For a deeper dive into this topic, check out [The Configuration Complexity Curse](https://blog.cedriccharly.com/post/20191109-the-configuration-complexity-curse/) and [How CUE Wins](https://blog.cedriccharly.com/post/20210523-how-cue-wins/).

## Just enough CUE

The goal of this doc page is to share just enough CUE for you to be comfortable writing Dagger configurations.

We will not rewrite the [CUE documentation](https://cuelang.org/docs/), or replicate the excellent [CUE tutorials](https://cuetorials.com/).

Instead, we will take 15 minutes to cover the following key concepts:

1. CUE is a JSON superset
2. Types _are_ values
3. Concrete values
4. Constraints, definitions &amp; schemas
5. Unification
6. Default values and inheritance
7. Packages

Before we start, you may want to [install CUE](https://github.com/cue-lang/cue#download-and-install).
Alternatively, you can [use the CUE playground](https://cuelang.org/play/) for the examples below.

### CUE is a JSON superset

What you can express in JSON, you can express in CUE.
Not everything in CUE can be expressed in JSON.
CUE also supports a "lite" version of JSON where certain characters can be eliminated entirely.
Consider the following code:

```cue
{
  "Bob": {
    "Name": "Bob Smith",
    "Age": 42
  }
}

Bob: Name: "Bob Smith"
```

In the above example we see that in CUE we have declared a top-level key _Bob_ twice:

1. In a more verbose JSON style with brackets, quotes, and commas
2. In a "short hand" style without the extra characters

[Try the above example in the CUE playground](https://cuelang.org/play/?id=qXGPCDqQdtp#cue@export@yaml).
Notice the different types of output: CUE, JSON & YAML.

The top-level Bob key is declared twice, but is only output once because CUE is automatically _unifying_ the two declarations.
It's OK to declare the same field multiple times, _as long as the value provided is the same_.

### Types _are_ values

In the previous example we defined:

- the `Name` value as the string literal `"Bob Smith"`
- the `Age` value as the integer literal `42`

Both `Name` and `Age` are _concrete values_ - we expand on this concept in the next section.

Usually the output of CUE will be used as input to some other system such as an API, a CLI tool (e.g. `dagger`), a CI environment, etc.
These systems will expect that data conforms to a schema where each field has a type, and is potentially constrained by a min, max, enums, regular expressions, etc.
With this in mind, we need to enforce _types_ and _constraints_ in order to prevent mistakes like setting the `Name` value to an integer, or the `Age` value to a string.

```cue
Bob: {
  Name: string // type as the value
  Age: int
}

Bob: {
  Name: "Bob Smith" // literals match the type
  Age: 42
}
```

Here we have defined the `Name` field as a `string` and the `Age` field as an `int`.
Notice how `string` and `int` _are not_ within quotes.
This is what we mean when we say "types _are_ values".
This will be quite familiar to anyone that has written Go or some other strongly-typed language.

With these types defined, CUE will now _enforce_ them, so that any attempt to provide say an integer for `Name`, or a string for `Age`, will result in an error.
It is worth mentioning that the output from this example is the result of _implicit unification_; we will talk about _explicit unification_ later.

[You can try the above example in the CUE playground](https://cuelang.org/play/?id=7iR-sFSEajk#cue@export@yaml).

### Concrete values

CUE is used to export data.
It is most useful when that data has been validated against a well-defined schema.
In order for CUE to export anything, we must provide _concrete values_ for all defined fields not marked as optional.
If we were to leave a required field simply defined as a type, without a concrete value, CUE will return an error:

```cue
Bob: {
  Name: string
  Age: int
}

Bob: {
  Name: "Bob Smith"
  //Age: is considered "incomplete" because no concrete value is defined
}
```

[Try the above in the CUE playground](https://cuelang.org/play/?id=cflqGMQsbLo#cue@export@yaml) and see that CUE will complain of an _incomplete value_.

### Definitions

In a real-world scenario, we will likely need to define more than one person.
We will also need to ensure that each person satisfies the same schema.
_Definitions_ help us achieve this:

```cue
#Person: {
  Name: string
  Email: string
  Age?: int
}

Bob: #Person & {
  Name: "Bob Smith"
  Email: "bob@smith.com"
  // Age is now optional
}
```

In this example we have declared that `#Person` is a _definition_, as denoted by the `#` sign.
By doing so, we have _constrained_ the Person object to a specific set of fields, each a specific type.
Definitions by default are _closed_ meaning that a `#Person` cannot contain any fields not specified in the definition.
You will also notice that `Age?` now contains a `?` which denotes this field is _optional_.

Definitions themselves are _not_ exported to final output.
To get concrete output, we have declared that the field `Bob` _is_ a `#Person`, and using the _single_ `&` - which is different from the logical AND `&&` - we _unified_ the `#Person` definition with an object whose _concrete values satisfy the constraints_ defined by that definition.

You can think of _definitions_ as a logical set of related _constraints_ and a _schema_ as a larger collective of constraints, not all of which need to be definitions.

[Try this example in the CUE playground](https://cuelang.org/play/?id=S-c7N0EZsYN#cue@export@yaml) and experiment with making fields optional via `?` with values both defined and not defined, and see how the behaviour changes.

### Unification

Unification is at the core of CUE - U stands for unification.

If values are the fuel, unification is the engine.
It is through unification that we can both define constraints and compute concrete values.
The following example shows this fundamental CUE principle in action:

```cue
import (
  "strings" // import builtin package
)           // more on packages later

#Person: {

  // further constrain to a min and max length
  Name: string & strings.MinRunes(3) & strings.MaxRunes(22)

  // we don't need string because the regex handles that
  Email: =~"^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$"

  // further constrain to realistic ages
  Age?: int & >0 & <140

}

Bob: #Person & {
  Name: "Bob Smith"
  Email: "bob@smith.com"
  Age: 42
}

// output in YAML:
Bob:
  Name: Bob Smith
  Email: bob@smith.com
  Age: 42

```

The output here is a product of _*unifying*_ the `#Person` _definition_ with an object that contains _concrete values_.
Each concrete value is the product of unifying the concrete value with the _types_ and _constraints_ declared by the field in the definition.
[Try it in the CUE playground](https://cuelang.org/play/?id=nAUx1-VlrY4#cue@export@yaml).

### Default values and inheritance

When unifying objects, or _structs_ as we call them in Go, a form of merging happens where fields are unified recursively.
Unlike merging JSON objects in JavaScript, differing values will _not override_ but result in an error.
This is a result of the [_commutative nature of CUE_](https://cuelang.org/docs/usecases/configuration/) - if order doesn't matter, how would we choose the "right" value?

Let us take a look at another example:

```cue
import (
  "strings" // a builtin package
)

#Person: {

  // further constrain to a min and max length
  Name: string & strings.MinRunes(3) & strings.MaxRunes(22)

  // we don't need string because the regex handles that
  Email: =~"^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$"

  // further constrain to realistic ages
  Age?: int & >0 & <140

  // Job is optional and a string
  Job?: string
}

#Engineer: #Person & {
  Job: "Engineer" // Job is further constrained to required and exactly this value
}


Bob: #Engineer & {
  Name: "Bob Smith"
  Email: "bob@smith.com"
  Age: 42
  // Job: "Carpenter" // would result in an error
}

// output in YAML:
Bob:
  Name: Bob Smith
  Email: bob@smith.com
  Age: 42
  Job: Engineer

```

While it is possible for the `Bob` object to inherit the Job value from `#Engineer` which in turn inherits constraints from `#Person`, it is _not possible to override the Job value_.
[Try this in the CUE playground](https://tip.cuelang.org/play/?id=_Cvwm6KeGZm#cue@export@yaml) and uncomment the `Job` field in `Bob` to see the error that CUE returns.

If we wanted the `Bob` object to have a different job, it would either need to be unified with a different type OR the `#Engineer:Job:` field would need a looser constraint with a _default value_.

Try changing the `Job` field to the following:

```cue
#Engineer: #Person & {
  Job: string | *"Engineer" // can still be any string,
                            // but *defaults* to "Engineer"
                            // when no concrete value is explicitly defined
}
```

With this change, `Bob` inherits the _default value_ and is allowed to specify a different job.

### Packages

Packages in CUE allow us to write _modular_, _reusable_, and _composable_ code.
We can define schemas that are _imported_ into various files and projects.
If you have written Go, then CUE should feel quite familiar.
Not only is CUE [written in Go](https://pkg.go.dev/cuelang.org/go@v0.4.0/cue#pkg-overview), but much of its behavior and syntax are modeled after Go as well.

CUE has a number of [builtin packages](https://pkg.go.dev/cuelang.org/go/pkg) such as `strings`, `regexp`, `math`, etc.
These builtin packages are already available to CUE without the need to download or install anything else.
Third-party packages are those that are placed within the `cue.mod/pkg/` folder and start with a fully qualified domain, e.g. `universe.dagger.io`

:::tip
Now that you have a solid CUE foundation, we will look at the Dagger CUE API, captured in the `dagger.io/dagger` package.
This is the core of Dagger that all other packages build on top of.
:::
