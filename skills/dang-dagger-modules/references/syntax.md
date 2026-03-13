# Dang Syntax Reference

This file is the grammar-focused reference for Dang syntax used by this skill.

## Canonical Sources

- Grammar source: `pkg/dang/dang.peg` in `github.com/vito/dang`
- Generated parser grammar: `treesitter/src/grammar.json` (same repo)
- Executable examples: `tests/test_*.dang` (same repo)

Last synced with upstream Dang commit: `83d35a8ba56e70037b67651d25de10734b5fcafe` (2026-03-04).

## Update Procedure

1. Fetch latest commit:
   - `git ls-remote https://github.com/vito/dang HEAD`
2. Clone that revision and inspect:
   - `pkg/dang/dang.peg`
   - `tests/test_*.dang` for real usage patterns
3. Update this file first (syntax only).
4. Update `SKILL.md` examples only if they conflict with this file.
5. Replace the `Last synced` commit line.
6. Spot-check by loading at least one Dang module:
   - `dagger call -m ./modules/<module> --help`

## Lexical Rules

- Line comments: `# ...`
- Forms are separated by newline or comma, depending on context.
- Identifiers:
  - `Id`: `[a-zA-Z_][a-zA-Z0-9_]*`
  - Type names (`UpperId`) start with uppercase
- Reserved keywords include:
  - `type`, `interface`, `union`, `enum`, `scalar`, `directive`
  - `pub`, `let`, `new`, `import`, `implements`
  - `if`, `else`, `for`, `case`, `try`, `catch`, `raise`, `assert`, `break`, `continue`

## Declarations

### Module bindings

```dang
pub description = "Example module"
let internal = "private value"
```

### Type declarations

```dang
type Site {
  pub path: String!
  pub url: String! {
    "https://example.com/" + path
  }
}
```

`type` can include `implements`:

```dang
type MyError implements Error {
  pub message: String!
}
```

### Slot/function forms inside `type`

```dang
pub name: String!                # field
pub retries: Int! = 3            # field with default
pub label = "stable"             # inferred type
pub build: Container! { ... }    # zero-arg function
pub run(cmd: String!): String! { ... }   # function with args
```

### Constructors

```dang
new(src: Directory!) {
  self.source = src
}
```

### Other declarations

```dang
import foo

interface Service {
  pub endpoint: String!
  pub call(path: String!): String!
}

union Result = Success | Failure

enum Status {
  READY
  RUNNING
  DONE
}

scalar Timestamp

directive @check on FIELD_DEFINITION
```

## Types

```dang
String          # nullable
String!         # non-null
[String!]!      # list
{{name: String!, count: Int!}}   # object type literal
```

Type hints are expressions with `::`:

```dang
[]::[String!]!
```

## Expressions and Calls

### Literals

```dang
"hello"             # string
"""doc"""           # triple-quoted string
42                  # int
3.14                # float
1.5e10              # float with exponent
true                # boolean
null
self
%re{^v[0-9]+$}      # quoted literal
```

### Collections and objects

```dang
[1, 2, 3]
{{name: "docs", enabled: true}}
```

### Calls and selection

```dang
fn("x")
fn("x", mode: "strict")
obj.method("x")
obj.field              # zero-arg autocall/select
obj.{name, id}         # object selection
```

Named arguments use `:` (not `=`).

### Block args

```dang
numbers.map { x => x * 2 }
numbers.filter { true }
```

User functions can declare typed block params:

```dang
pub twice(&block(x: Int!): Int!): Int! {
  block(1) + block(2)
}
```

## Control Flow and Errors

### Conditionals

```dang
if (cond) {
  "yes"
} else {
  "no"
}
```

### Loops

```dang
let i = 0
for (i < 3) {
  i += 1
}

for {
  break
}
```

### Case expressions

```dang
case (value) {
  "a" => 1
  else => 0
}

case {
  cond1 => "x"
  cond2 => "y"
}
```

Typed patterns are supported:

```dang
case (err) {
  e: ValidationError => e.message
  else => "unknown"
}
```

### Error handling

```dang
try {
  raise "boom"
} catch {
  err => err.message
}

assert { value != null }
break
continue
```

## Operators

Precedence (lowest to highest):

1. `??`
2. `or`
3. `and`
4. `==`, `!=`
5. `<`, `<=`, `>`, `>=`
6. `+`, `-`
7. `*`, `/`, `%`
8. unary `!`

Reassignment operators:

- `=`
- `+=`

## Common Syntax Pitfalls

- `=` is not used for named call arguments.
- Object literals/types use `{{...}}`, not `{...}`.
- `for (x in xs)` is not a language loop form; use list methods like `.each`/`.map`, or `for (cond)` loops.
