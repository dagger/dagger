# GraphQL Schema Code Generator

This is the beginning of a tool for auto-generating the base layer of Dagger SDK.
It runs an introspection query against the Dagger enging and parses the result.
The goal (not yet implemnented) is to generate base C# classes from the GraphQL type definitions.

## What does it do now?

- Connects to Dagger engine and runs the introspection query.
- Parses the Types and Directive data from the result
- Reports number of 
- Outputs two files (for debug and comparison):
  - JSON serialization of the data directly from the GraphQL API
  - JSON serialization of the data after being parsed into Object
- Observing that these two files are identical confirms that parsing worked correctly

## How to run

```
dagger run -- dotnet run
```
