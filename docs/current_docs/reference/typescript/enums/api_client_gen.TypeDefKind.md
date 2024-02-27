---
id: "api_client_gen.TypeDefKind"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).TypeDefKind

Distinguishes the different kinds of TypeDefs.

## Enumeration Members

### BooleanKind

 **BooleanKind** = ``"BOOLEAN_KIND"``

A boolean value.

___

### InputKind

 **InputKind** = ``"INPUT_KIND"``

A graphql input type, used only when representing the core API via TypeDefs.

___

### IntegerKind

 **IntegerKind** = ``"INTEGER_KIND"``

An integer value.

___

### InterfaceKind

 **InterfaceKind** = ``"INTERFACE_KIND"``

A named type of functions that can be matched+implemented by other objects+interfaces.

Always paired with an InterfaceTypeDef.

___

### ListKind

 **ListKind** = ``"LIST_KIND"``

A list of values all having the same type.

Always paired with a ListTypeDef.

___

### ObjectKind

 **ObjectKind** = ``"OBJECT_KIND"``

A named type defined in the GraphQL schema, with fields and functions.

Always paired with an ObjectTypeDef.

___

### StringKind

 **StringKind** = ``"STRING_KIND"``

A string value.

___

### VoidKind

 **VoidKind** = ``"VOID_KIND"``

A special kind used to signify that no value is returned.

This is used for functions that have no return value. The outer TypeDef specifying this Kind is always Optional, as the Void is never actually represented.
