# How to Add a New Pragma/Decorator to the TypeScript SDK

This guide walks through adding a new decorator (TypeScript's equivalent of Go pragmas) that integrates with the Dagger GraphQL API.

## Overview

Adding a decorator like `@myFeature()` requires changes across 5 files in the TypeScript SDK. The decorator system works by:

1. **Defining** the decorator in the registry (runtime no-op)
2. **Exporting** it for public use
3. **Parsing** it during introspection via the TypeScript AST
4. **Storing** the parsed data in the introspection model
5. **Registering** it with the Dagger API via GraphQL calls

---

## Prerequisites

Before adding a TypeScript decorator, you must:

1. **Add the GraphQL directive** in `dagql/server.go` (see Go pragma guide)
2. **Add the API resolver** in `core/schema/module.go` (e.g., `functionWithMyFeature`)
3. **Regenerate the SDK** so the new API (e.g., `withMyFeature()`) is available

---

## File Structure

All TypeScript SDK decorator code lives under `sdk/typescript/src/module/`:

```
sdk/typescript/src/module/
├── decorators.ts              # Public exports
├── registry.ts                # Decorator definitions
└── introspector/
    ├── dagger_module/
    │   ├── decorator.ts       # Decorator constants
    │   ├── function.ts        # Function introspection (for @func-like decorators)
    │   └── argument.ts        # Argument introspection (for @argument-like decorators)
    └── entrypoint/
        └── register.ts        # API registration
```

---

## Step-by-Step Implementation

### Example: Adding `@check()` Decorator

We'll use `@check()` as an example - a function-level decorator with no arguments.

---

### 1. Define the Decorator in Registry

**File:** `sdk/typescript/src/module/registry.ts`

Add the decorator method to the `Registry` class. This is a no-op at runtime - decorators are only analyzed during introspection.

```typescript
export class Registry {
  // ... existing decorators ...

  /**
   * The definition of @check decorator that marks a function as a check.
   */
  check = (): ((
    target: object,
    propertyKey: string | symbol,
    descriptor?: PropertyDescriptor,
  ) => void) => {
    return (
      target: object,
      propertyKey: string | symbol,
      descriptor?: PropertyDescriptor,
    ) => {}
  }
}
```

**For decorators with options:**

```typescript
export type CheckOptions = {
  /**
   * Optional timeout for the check.
   */
  timeout?: string
}

export class Registry {
  check = (
    opts?: CheckOptions,
  ): ((
    target: object,
    propertyKey: string | symbol,
    descriptor?: PropertyDescriptor,
  ) => void) => {
    return (
      target: object,
      propertyKey: string | symbol,
      descriptor?: PropertyDescriptor,
    ) => {}
  }
}
```

---

### 2. Export the Decorator Publicly

**File:** `sdk/typescript/src/module/decorators.ts`

Export the decorator so users can import it:

```typescript
/**
 * The definition of @check decorator that marks a function as a check.
 * Checks are functions that return void/error to indicate pass/fail.
 */
export const check = registry.check
```

---

### 3. Add Decorator Constant

**File:** `sdk/typescript/src/module/introspector/dagger_module/decorator.ts`

Add a constant for the decorator name and update the type:

```typescript
import { argument, func, object, enumType, field, check } from "../../decorators.js"

export type DaggerDecorators =
  | "object"
  | "func"
  | "check"      // ADD THIS
  | "argument"
  | "enumType"
  | "field"

export const OBJECT_DECORATOR = object.name as DaggerDecorators
export const FUNCTION_DECORATOR = func.name as DaggerDecorators
export const CHECK_DECORATOR = check.name as DaggerDecorators  // ADD THIS
export const FIELD_DECORATOR = field.name as DaggerDecorators
export const ARGUMENT_DECORATOR = argument.name as DaggerDecorators
export const ENUM_DECORATOR = enumType.name as DaggerDecorators
```

---

### 4. Parse Decorator During Introspection

**For function-level decorators:**

**File:** `sdk/typescript/src/module/introspector/dagger_module/function.ts`

Add a field to store the parsed value and parse it in the constructor:

```typescript
import { CHECK_DECORATOR } from "./decorator.js"

export class DaggerFunction extends Locatable {
  public name: string
  public description: string
  public deprecated?: string
  // ... existing fields ...
  public isCheck: boolean = false  // ADD THIS

  constructor(
    private readonly node: ts.MethodDeclaration,
    private readonly ast: AST,
  ) {
    super(node)

    // ... existing parsing ...

    // Parse @check decorator
    if (this.ast.isNodeDecoratedWith(this.node, CHECK_DECORATOR)) {
      this.isCheck = true
    }
  }
}
```

**For decorators with options:**

```typescript
import { CheckOptions } from "../../registry.js"

export class DaggerFunction extends Locatable {
  public timeout?: string

  constructor(
    private readonly node: ts.MethodDeclaration,
    private readonly ast: AST,
  ) {
    super(node)

    // Parse @check decorator with options
    const checkOptions = this.ast.getDecoratorArgument<CheckOptions>(
      this.node,
      CHECK_DECORATOR,
      "object",
    )
    if (checkOptions) {
      this.timeout = checkOptions.timeout
    }
  }
}
```

**For argument-level decorators:**

**File:** `sdk/typescript/src/module/introspector/dagger_module/argument.ts`

Similar pattern - add fields and parse in constructor. See existing `@argument` decorator for reference.

---

### 5. Register with Dagger API

**File:** `sdk/typescript/src/module/entrypoint/register.ts`

Call the generated API method during registration:

**For function-level decorators:**

```typescript
import {
  // ... existing imports ...
  FunctionWithCheckOpts,
} from "../../api/client.gen.js"

export class Register {
  addFunction(fct: Method | DaggerInterfaceFunction): Function_ {
    let fnDef = dag
      .function_(fct.alias ?? fct.name, addTypeDef(fct.returnType!))
      .withDescription(fct.description)
      .withSourceMap(addSourceMap(fct))
      .with(this.addArg(fct.arguments))

    // ... existing cache policy, deprecated handling ...

    // ADD THIS
    if ((fct as Method).isCheck) {
      fnDef = fnDef.withCheck()
    }

    return fnDef
  }
}
```

**With options:**

```typescript
if ((fct as Method).isCheck) {
  const opts: FunctionWithCheckOpts = {}
  if ((fct as Method).timeout) {
    opts.timeout = (fct as Method).timeout
  }
  fnDef = fnDef.withCheck(opts)
}
```

**For argument-level decorators:**

Modify the `addArg` method instead of `addFunction`.

---

## Decorator Patterns

### Pattern 1: Simple Boolean Flag

```typescript
// registry.ts
check = (): DecoratorFunction => {
  return () => {}
}

// function.ts
if (this.ast.isNodeDecoratedWith(this.node, CHECK_DECORATOR)) {
  this.isCheck = true
}

// register.ts
if ((fct as Method).isCheck) {
  fnDef = fnDef.withCheck()
}
```

### Pattern 2: Single String Argument

```typescript
// registry.ts
alias = (name: string): DecoratorFunction => {
  return () => {}
}

// function.ts
const aliasArg = this.ast.getDecoratorArgument<string>(
  this.node,
  ALIAS_DECORATOR,
  "string",
)
if (aliasArg) {
  this.alias = aliasArg.replace(/['"]/g, '') // Remove quotes
}

// register.ts
fnDef = dag.function_(fct.alias ?? fct.name, ...)
```

### Pattern 3: Options Object

```typescript
// registry.ts
export type MyFeatureOptions = {
  enabled?: boolean
  value?: string
}

myFeature = (opts?: MyFeatureOptions): DecoratorFunction => {
  return () => {}
}

// function.ts
const options = this.ast.getDecoratorArgument<MyFeatureOptions>(
  this.node,
  MY_FEATURE_DECORATOR,
  "object",
)
if (options) {
  this.myFeatureEnabled = options.enabled ?? true
  this.myFeatureValue = options.value
}

// register.ts
if ((fct as Method).myFeatureEnabled) {
  const opts: FunctionWithMyFeatureOpts = {}
  if ((fct as Method).myFeatureValue) {
    opts.value = (fct as Method).myFeatureValue
  }
  fnDef = fnDef.withMyFeature(opts)
}
```

---

## AST Helper Methods

The `AST` class provides two key methods for parsing decorators:

### `isNodeDecoratedWith(node, decorator)`

Checks if a node has a specific decorator:

```typescript
if (this.ast.isNodeDecoratedWith(this.node, CHECK_DECORATOR)) {
  this.isCheck = true
}
```

### `getDecoratorArgument<T>(node, decorator, type, position?)`

Extracts decorator arguments:

```typescript
// For string arguments: @myDecorator("value")
const value = this.ast.getDecoratorArgument<string>(
  this.node,
  MY_DECORATOR,
  "string",
  0,  // position (default: 0)
)

// For object arguments: @myDecorator({ key: "value" })
const options = this.ast.getDecoratorArgument<MyOptions>(
  this.node,
  MY_DECORATOR,
  "object",
)
```

**Note:** The "object" type uses `eval()` internally, so it only works with object literals, not variables or complex expressions.

---

## Usage Example

Once implemented, users can use the decorator:

```typescript
import { object, func, check } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  @check()
  async passingCheck(): Promise<void> {
    await dag.container()
      .from("alpine:3")
      .withExec(["sh", "-c", "exit 0"])
      .sync()
  }
}
```

**With options:**

```typescript
@func()
@check({ timeout: "5m" })
async slowCheck(): Promise<void> {
  // ...
}
```

---

## Testing

1. **Add test module** in `sdk/typescript/src/module/introspector/test/testdata/`
2. **Add introspection test** to verify parsing
3. **Add integration test** to verify API registration
4. **Regenerate SDK** to pick up new API methods

---

## Common Gotchas

1. **Import the decorator constant** in the introspection file (e.g., `CHECK_DECORATOR`)
2. **Add type imports** for options types (e.g., `CheckOptions`)
3. **Cast to specific type** when accessing decorator data: `(fct as Method).isCheck`
4. **Regenerate SDK** after adding GraphQL APIs before implementing TypeScript side
5. **Decorator arguments** must be literals - variables won't work due to `eval()`
6. **Empty decorators** still need parentheses: `@check()` not `@check`

---

## Comparison: TypeScript vs Go

| Aspect | TypeScript | Go |
|--------|-----------|-----|
| Syntax | `@check()` | `// +check` |
| Location | Above function | In comment above function |
| Options | `@check({ timeout: "5m" })` | `// +check:timeout=5m` |
| Detection | AST parsing during introspection | Regex parsing of comments |
| Runtime | No-op | No-op |
| Type safety | TypeScript types for options | JSON/string parsing |

---

## Reference Examples

- **Simple flag**: `@check()` (this guide)
- **String argument**: `@func("alias")` 
- **Options object**: `@argument({ defaultPath: "./file" })`
- **Cache policy**: `@func({ cache: "never" })`

---

## Further Reading

- [Go Pragma Guide](./go-pragma-guide.md) - How to add the backend GraphQL directive
- [TypeScript Decorators](https://www.typescriptlang.org/docs/handbook/decorators.html) - Official TS docs
- [Dagger Module System](https://docs.dagger.io/api/modules) - High-level overview
