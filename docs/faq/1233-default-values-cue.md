---
slug: /1233/default-values-cue
displayed_sidebar: '0.2'
---

# Default values and optional fields

When writing a Cue config, you will sometimes want to set default values in your package.

The most common way you'll encounter in our codebase is: `key: type | *value`:

```cue
defaultValue: string | *"foo"
defaultValue: bool | *false
```

You'll also encounter the `*null` default value, which is self explanatory:

```cue
// here, defaultValue either accepts a #PersonalDefinition, or stays null by default
defaultValue: #PersonalDefinition | *null
```

To test the type of `defaultValue`, you can directly do such assertion:

```cue
if defaultValue != "foo" | if defaultValue != false | if defaultValue != null {
    ...
}
 
if defaultValue == "foo" | if defaultValue == false | if defaultValue == null {
    ...
}
```

However, don't get confused with the optional fields. Optional fields check whether a key is concrete at the given scope in the DAG. You declare them with `?` at the end of their name: `foo?`.

```cue
foo?: string // 1. declare foo. It remains undefined for now

foo: "bar" // 2. Now, `foo` gets concrete. The field isn't undefined anymore
```

To check on a field's concreteness, use the bottom value `_|_`:

```cue
if foo != _|_ { // if foo is not `undefined`
    ...
}
 
if foo == _|_ { // if foo is `undefined`
    ...
}
```
