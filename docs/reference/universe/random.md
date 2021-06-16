---
sidebar_label: random
---

# dagger.io/random

Random generation utilities

```cue
import "dagger.io/random"
```

## random.#String

Generate a random string

### random.#String Inputs

| Name             | Type               | Description                                                                                                                       |
| -------------    |:-------------:     |:-------------:                                                                                                                    |
|*seed*            | `string`           |Seed of the random string to generate. When using the same `seed`, the same random string will be generated because of caching.    |
|*length*          | `*12 \| number`    |length of the string                                                                                                               |

### random.#String Outputs

| Name             | Type              | Description               |
| -------------    |:-------------:    |:-------------:            |
|*out*             | `string`          |generated random string    |
