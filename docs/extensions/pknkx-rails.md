---
slug: /pknkx/rails
displayed_sidebar: '0.3'
---

# Rails

A Dagger extension for Rails operations.

## Example

```
{
  host {
    workdir {
      read {
        rails(runArgs: ["test:all"]) {
          id
        }
      }
    }
  }
}
```

## Links

- [GitHub](https://github.com/kpenfound/dagger-rails)