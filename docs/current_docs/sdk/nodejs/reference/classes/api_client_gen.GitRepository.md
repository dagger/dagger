---
id: "api_client_gen.GitRepository"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).GitRepository

A git repository.

## Hierarchy

- `BaseClient`

  ↳ **`GitRepository`**

## Constructors

### constructor

**new GitRepository**(`parent?`, `_id?`): [`GitRepository`](api_client_gen.GitRepository.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`GitRepositoryID`](../modules/api_client_gen.md#gitrepositoryid) |

#### Returns

[`GitRepository`](api_client_gen.GitRepository.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`GitRepositoryID`](../modules/api_client_gen.md#gitrepositoryid) = `undefined`

## Methods

### branch

**branch**(`name`): [`GitRef`](api_client_gen.GitRef.md)

Returns details on one branch.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | Branch's name (e.g., "main"). |

#### Returns

[`GitRef`](api_client_gen.GitRef.md)

___

### commit

**commit**(`id`): [`GitRef`](api_client_gen.GitRef.md)

Returns details on one commit.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `id` | `string` | Identifier of the commit (e.g., "b6315d8f2810962c601af73f86831f6866ea798b"). |

#### Returns

[`GitRef`](api_client_gen.GitRef.md)

___

### id

**id**(): `Promise`\<[`GitRepositoryID`](../modules/api_client_gen.md#gitrepositoryid)\>

Retrieves the content-addressed identifier of the git repository.

#### Returns

`Promise`\<[`GitRepositoryID`](../modules/api_client_gen.md#gitrepositoryid)\>

___

### tag

**tag**(`name`): [`GitRef`](api_client_gen.GitRef.md)

Returns details on one tag.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | Tag's name (e.g., "v0.3.9"). |

#### Returns

[`GitRef`](api_client_gen.GitRef.md)
