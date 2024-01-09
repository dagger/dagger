---
id: "api_client_gen.TypeDefKind"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).TypeDefKind

Distinguishes the different kinds of TypeDefs.

## Enumeration Members

### Booleankind

 **Booleankind** = ``"BooleanKind"``

A boolean value

___

### Integerkind

 **Integerkind** = ``"IntegerKind"``

An integer value

___

### Interfacekind

 **Interfacekind** = ``"InterfaceKind"``

A named type of functions that can be matched+implemented by other objects+interfaces.

Always paired with an InterfaceTypeDef.

___

### Listkind

 **Listkind** = ``"ListKind"``

A list of values all having the same type.

Always paired with a ListTypeDef.

___

### Objectkind

 **Objectkind** = ``"ObjectKind"``

A named type defined in the GraphQL schema, with fields and functions.

Always paired with an ObjectTypeDef.

___

### Stringkind

 **Stringkind** = ``"StringKind"``

A string value

___

### Voidkind

 **Voidkind** = ``"VoidKind"``

A special kind used to signify that no value is returned.

This is used for functions that have no return value. The outer TypeDef
specifying this Kind is always Optional, as the Void is never actually
represented.
