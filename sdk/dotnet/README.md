> **Warning** This SDK is experimental. Please do not use it for anything
> mission-critical. Possible issues include:

- Missing features
- Stability issues
- Performance issues
- Lack of polish
- Upcoming breaking changes
- Incomplete or out-of-date documentation

# Dagger.SDK

Dagger SDK for Microsoft .NET.

## Development

This project provides `dev` module to uses for developing the SDK.

### Introspection

Uses for fetching introspection by using the command:

```
$ dagger -m dev introspect export --path=./sdk/Dagger.SDK/introspection.json
```

### Test

You can running all tests by:

```
$ dagger -m dev test --source=.
```
