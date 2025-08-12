# Predicate type: Dagger Trace URL

Type URI: https://dagger.io/cloud

Version: 0.1

Predicate Name: Trace URL

## Purpose

A reference to a Dagger Cloud trace URL

## Use Cases

Associate a build or artifact with the Dagger Cloud trace that produced it

## Prerequisites

A [Dagger Cloud](https://dagger.io/cloud) account. The trace URL of a run can be found during a session from the Dagger client at Client.Cloud.TraceURL

## Model

This is a predicate type that fits within the larger Attestation framework.

## Schema

```jsonc
{
  // Standard attestation fields:
  "_type": "https://in-toto.io/Statement/v1",
  "subject": [{ ... }],

  // Predicate:
  "predicateType": "https://dagger.io/evidence/trace-url/v1",
  "predicate": {
    "traceURL": "https://dagger.cloud/traces/abcd1234"
  }
}
```

### Parsing Rules

Parsing rules are important to define explicitly to ensure implementations can
handle example attestations correctly. For example, this section can discuss how
the predicate type is versioned, how non-specified fields must be handled, and
so on. Attestations definitions MUST use this section to define how the parsing
rules differ from the framework’s
[standard parsing rules](/spec/v1/README.md#parsing-rules).

### Fields

`traceURL`: A valid url to a Dagger Cloud trace

## Example

```jsonc
{
  // Standard attestation fields:
  "_type": "https://in-toto.io/Statement/v1",
  "subject": [{ ... }],

  // Predicate:
  "predicateType": "https://dagger.io/evidence/trace-url/v1",
  "predicate": {
    "traceURL": "https://dagger.cloud/traces/abcd1234"
  }
}
```

## Changelog and Migrations

This is the initial version
