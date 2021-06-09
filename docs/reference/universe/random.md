---
sidebar_label: random
---

# dagger.io/random

Random generation utilities.

Example:

```cue
str: random.#String & {
  seed: "str"
  length: 10
}
```

## #String

Generate a random string

### #String Inputs

| Name             | Type               | Description                                                                                                                       |
| -------------    |:-------------:     |:-------------:                                                                                                                    |
|*seed*            | `string`           |Seed of the random string to generate. When using the same `seed`, the same random string will be generated because of caching.    |
|*length*          | `*12 \| number`    |length of the string                                                                                                               |

### #String Outputs

| Name             | Type              | Description               |
| -------------    |:-------------:    |:-------------:            |
|*out*             | `string`          |generated random string    |
