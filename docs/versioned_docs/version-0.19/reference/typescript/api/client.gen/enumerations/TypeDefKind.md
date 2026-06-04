[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / TypeDefKind

# Enumeration: TypeDefKind

Distinguishes the different kinds of TypeDefs.

## Enumeration Members

### Boolean

> **Boolean**: `"BOOLEAN_KIND"`

A boolean value.

***

### BooleanKind

> **BooleanKind**: `"BOOLEAN_KIND"`

A boolean value.

***

### Enum

> **Enum**: `"ENUM_KIND"`

A GraphQL enum type and its values

Always paired with an EnumTypeDef.

***

### EnumKind

> **EnumKind**: `"ENUM_KIND"`

A GraphQL enum type and its values

Always paired with an EnumTypeDef.

***

### Float

> **Float**: `"FLOAT_KIND"`

A float value.

***

### FloatKind

> **FloatKind**: `"FLOAT_KIND"`

A float value.

***

### Input

> **Input**: `"INPUT_KIND"`

A graphql input type, used only when representing the core API via TypeDefs.

***

### InputKind

> **InputKind**: `"INPUT_KIND"`

A graphql input type, used only when representing the core API via TypeDefs.

***

### Integer

> **Integer**: `"INTEGER_KIND"`

An integer value.

***

### IntegerKind

> **IntegerKind**: `"INTEGER_KIND"`

An integer value.

***

### Interface

> **Interface**: `"INTERFACE_KIND"`

Always paired with an InterfaceTypeDef.

A named type of functions that can be matched+implemented by other objects+interfaces.

***

### InterfaceKind

> **InterfaceKind**: `"INTERFACE_KIND"`

Always paired with an InterfaceTypeDef.

A named type of functions that can be matched+implemented by other objects+interfaces.

***

### List

> **List**: `"LIST_KIND"`

Always paired with a ListTypeDef.

A list of values all having the same type.

***

### ListKind

> **ListKind**: `"LIST_KIND"`

Always paired with a ListTypeDef.

A list of values all having the same type.

***

### Object

> **Object**: `"OBJECT_KIND"`

Always paired with an ObjectTypeDef.

A named type defined in the GraphQL schema, with fields and functions.

***

### ObjectKind

> **ObjectKind**: `"OBJECT_KIND"`

Always paired with an ObjectTypeDef.

A named type defined in the GraphQL schema, with fields and functions.

***

### Scalar

> **Scalar**: `"SCALAR_KIND"`

A scalar value of any basic kind.

***

### ScalarKind

> **ScalarKind**: `"SCALAR_KIND"`

A scalar value of any basic kind.

***

### String

> **String**: `"STRING_KIND"`

A string value.

***

### StringKind

> **StringKind**: `"STRING_KIND"`

A string value.

***

### Void

> **Void**: `"VOID_KIND"`

A special kind used to signify that no value is returned.

This is used for functions that have no return value. The outer TypeDef specifying this Kind is always Optional, as the Void is never actually represented.

***

### VoidKind

> **VoidKind**: `"VOID_KIND"`

A special kind used to signify that no value is returned.

This is used for functions that have no return value. The outer TypeDef specifying this Kind is always Optional, as the Void is never actually represented.
