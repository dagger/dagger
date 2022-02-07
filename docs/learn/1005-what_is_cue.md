---
slug: /1005/what-is-cue/
---

# What is Cue?

CUE is a powerful configuration language created by Marcel van Lohuizen who co-created the Borg Configuration Language (BCL)&mdash;the [language used to deploy all applications at Google](https://storage.googleapis.com/pub-tools-public-publication-data/pdf/43438.pdf). CUE is the result of years of experience writing configuration languages at Google, and seeks to improve the developer experience while avoiding some nasty pitfalls. It is a superset of JSON, with additional features to make declarative, data-driven programming as pleasant and productive as regular imperative programming.

## The Need for a Configuration Language

For decades, developers, engineers, and system administrators alike have used some combination of `INI`, `ENV`, `YAML`, `XML`, and `JSON` (as well as custom formats such as those for Apache, Nginx, et al) to describe configurations, resources, operations, variables, parameters, state, etc. While these examples work fine for storing data, they are merely _data formats_, not languages, and as such they each lack the ability to execute logic and operate on data directly.

Simple&mdash;yet powerful!&mdash;things like if statements, for loops, comprehensions, and string interpolation, among others are just not possible in these formats without the use of a separate process for execution. The result is that variables or parameters must be injected, and any logic executed by a templating language (such as Jinja) or by a separate engine instructed by a DSL (Domain-specific Language). Often templating languages and DSLs are used in conjunction and while this technically works, the results are that we end up with code bases, or even single files, that are overly verbose, that intersperse templating languages with various DSLs (and sometimes multiple DSLs that feed output from one to the input of another!), that create rigid structures without enforcing schemas (not without more effort), thereby making the code challenging to reason about, difficult to maintain, brittle, and perhaps worst of all, _prone to side effects_.

A _configuration language_ such as CUE, allows us to both _specify_ data as well as _act_ upon that data with any logic necessary to achieve the desired output. Furthermore, and perhaps most importantly, CUE allows us to not only specify data as concrete values, but also specify the _types_ those concrete values must be as well as any _constraints_ such as min and max for example. It gives us the ability to define a _schema_ but unlike doing so with say JSON Schema, CUE can both _define_ and _enforce_ the schema, whereas JSON Schema is merely a definition that requires some other process to enforce it.

For a deeper dive on the problems with configuration as code, check out [The Configuration Complexity Curse](https://blog.cedriccharly.com/post/20191109-the-configuration-complexity-curse/) and [How Cue Wins](https://blog.cedriccharly.com/post/20210523-how-cue-wins/).

## Understanding Cue

We won't attempt to rewrite the [CUE documentation](https://cuelang.org/docs/) or replicate some excellent [CUE tutorials](https://cuetorials.com/), but instead give you enough understanding of CUE to move through the dagger tutorials.

At least for our purposes here you need to understand a few key concepts, each of which will be explained in more detail below:

1. Cue is a superset of JSON
2. Types _are_ values
3. Concrete values
4. Constraints, definitions, and schemas
5. Unification
6. Default values and the nature of inheritance
7. Packages

It would be most helpful to [install CUE](https://github.com/cue-lang/cue#download-and-install), but if you prefer you can also try these examples in the [CUE playground](https://cuelang.org/play/).

### Cue is a Superset of JSON

What you can express in JSON, you can express in CUE, but not everything in CUE can be expressed in JSON. CUE also supports a "lite" version of JSON where certain characters can be eliminated entirely. Take a look at the following code:

```cue
{
  "Bob": {
    "Name": "Bob Smith",
    "Age": 42
  }
}

Bob: Name: "Bob Smith"
```

In this example we see that in CUE we have declared a top-level key _Bob_ twice: once in a more verbose JSON style with brackets, quotes, and commas, and again with a "lite" style without the extra characters. Notice also that CUE supports _short hand_: when you are targeting a single key within an object, you don't need the curly braces, you can write it as a colon-separated path. [Try it in the CUE playground](https://cuelang.org/play/?id=qXGPCDqQdtp#cue@export@yaml), and notice the output (you can choose different formats). The top-level Bob key is declared twice, but is only output once because CUE is automatically _unifying_ the two declarations. It&rsquo;s ok to declare the same field multiple times, _so long as we provide the same value_. See [Default Values and the Nature of Inheritance](#default-values-and-the-nature-of-inheritance) below.

### Types _are_ Values

In the previous example we defined the `Name` value as the string literal _"Bob Smith"_ and the `Age` value as the integer literal _42_, both of which are [_concrete values_](#concrete-values). Generally, the output of CUE will be used as input to some other system be it an API, a CLI tool such as `dagger`, a CICD process, etc, and those systems will likely expect that the data conform to a schema where each field has a type and potentially constrained by such functions as min, max, enums, regular expressions, and so on. With that in mind, we need to enforce _types_ and _constraints_ in order to prevent us for example from setting the `Name` value as an integer, or the `Age` value as a string

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

Here we&rsquo;ve defined the `Name` field as a `string` and the `Age` field as an `int`. Notice how `string` and `int` _are not_ within quotes. This is what we mean when we say "types _are_ values". This will be quite familiar to anyone who has written Go or some other strongly-typed language. With these types defined, CUE will now _enforce_ them, so that any attempt to provide say an integer for the Name or a string for the Age will result in an error. It&rsquo;s worth noting here that the output from this example is the result of _implicit unification_; we&rsquo;ll talk about _explicit unification_ later. [Try it in the CUE playground](https://cuelang.org/play/?id=7iR-sFSEajk#cue@export@yaml).

### Concrete Values

CUE is ultimately used to export data, and is most useful when that data has been validated against a well-defined schema. In order for CUE to export anything, we must provide _concrete values_ for all defined fields not marked as optional. If we were to leave a required field simply defined as a type, without a concrete value, CUE will return an error.

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

[Try it in the CUE playground](https://cuelang.org/play/?id=cflqGMQsbLo#cue@export@yaml) and see that CUE will complain of an _incomplete value_.

### Definitions

In a real-world scenario we&rsquo;d likely need to define more than one person, and ensure that each one satisfies the schema. That's where `definitions` come in handy.

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

In this example we&rsquo;ve declared that `#Person` is a _definition_, as denoted by the `#` sign. By doing so, we have _constrained_ the Person object to a specific set of fields, each a specific type. Definitions by default are _closed_ meaning that a `#Person` cannot contain any fields not specified in the definition. You will also notice that `Age?` now contains a `?` which denotes this field as being _optional_.

Definitions themselves are _not_ exported to final output. To get concrete output, we&rsquo;ve declared that the field `Bob` _is_ a `#Person`, and using the _single_ `&` (not the same as logical AND via `&&`!) we _unified_ the `#Person` definition with an object whose _concrete values satisfy the constraints_ defined by that definition.

You can think of _definitions_ as a logical set of related _constraints_ and a _schema_ as a larger collective of constraints, not all of which need to be definitions.

[Try it in the CUE playground](https://cuelang.org/play/?id=S-c7N0EZsYN#cue@export@yaml) and experiment with making fields optional via `?` with values both defined and not defined to see.

### Unification

Unification is really at the core of what makes CUE what it is. If values are the fuel, unification is the engine. It is through unification that we can both define constraints and compute concrete values. Let's take a look at some examples to see this idea in action:

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

The output here is a product of _*unifying*_ the `#Person` _definition_ with an object that contains _concrete values_ each of which is the product of unifying the concrete value with the _types_ and _constraints_ declared by the field in the definition. [Try it in the CUE playground](https://cuelang.org/play/?id=nAUx1-VlrY4#cue@export@yaml)

### Default Values and the Nature of Inheritance

When unifying objects, or _structs_ as we like to call them, a form of merging happens where fields are unified recursively, but unlike for example merging JSON objects in JavaScript, differing values will _not override_ but result in an error. This is partially due to the [_commutative nature of CUE_](https://cuelang.org/docs/usecases/configuration/) (if order doesn't matter how would you choose one value over another?), but it is primarily due to the fact that overrides too easily result in unwanted and difficult to debug side effects. Let's take a look at another example:

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

While it's possible for the Bob object to inherit the Job value from `#Engineer` which in turn inherits constraints from `#Person`, it is _not possible to override the Job value_. [Try it in the CUE playground](https://tip.cuelang.org/play/?id=_Cvwm6KeGZm#cue@export@yaml) and uncomment the Job field in Bob to see that CUE returns an error.

If we wanted the Bob object to have a different job, it would either need to be unified with a different type OR the `#Engineer:Job:` field would need a looser constraint with a _default value_. Try changing the Job field to the following:

```cue
#Engineer: #Person & {
  Job: string | *"Engineer" // can still be any string, but *defaults* to "Engineer" when no concrete value is explicitly defined
}
```

Bob inherits the _default value_ but is now allowed to specify a different job.

### Packages

Packages in CUE allow us to write _modular_, _reusable_, and _composable_ code. We can define schemas that are _imported_ into various files and projects. If you&rsquo;ve written Go, then CUE should feel quite familiar. Not only is it [written in Go](https://pkg.go.dev/cuelang.org/go@v0.4.0/cue#pkg-overview) much of its behavior and syntax are modeled after Go as well.

CUE has a number of [builtin packages](https://pkg.go.dev/cuelang.org/go/pkg) such as `strings`, `regexp`, `math`, and many more. These builtin packages are already available to CUE without the need to download or install anything else. Third-party packages are those that are placed within the `cue.mod/pkg/` folder and start with a fully qualified domain such as `alpha.dagger.io`.

In the last few examples we&rsquo;ve included an `import` statement to load the builtin `strings` package. In the tutorials and other examples you will notice that other packages from `alpha.dagger.io` will be imported and used.
