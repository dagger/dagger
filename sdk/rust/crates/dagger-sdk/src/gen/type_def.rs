//! Generated bindings owned by the GraphQL `TypeDef` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "A definition of a parameter or return type in a Module."]
#[derive(Clone)]
pub struct TypeDef {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `TypeDef.withEnum`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct TypeDefWithEnumOpts {
    #[doc = "A doc string for the enum, if any\n\n`None` omits GraphQL Wire_Name `description` and preserves engine default `String(\"\")`."]
    pub description: Option<String>,
    #[doc = "The source map for the enum definition.\n\n`None` omits GraphQL Wire_Name `sourceMap`."]
    pub source_map: Option<crate::IdInput<super::SourceMap>>,
}
impl TypeDefWithEnumOpts {
    #[doc = "Sets GraphQL argument `description` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_description(mut self, value: impl Into<String>) -> Self {
        self.description = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `sourceMap` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_source_map(mut self, value: crate::IdInput<super::SourceMap>) -> Self {
        self.source_map = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `TypeDef.withEnumMember`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct TypeDefWithEnumMemberOpts {
    #[doc = "If deprecated, the reason or migration path.\n\n`None` omits GraphQL Wire_Name `deprecated`."]
    pub deprecated: Option<String>,
    #[doc = "A doc string for the member, if any\n\n`None` omits GraphQL Wire_Name `description` and preserves engine default `String(\"\")`."]
    pub description: Option<String>,
    #[doc = "The source map for the enum member definition.\n\n`None` omits GraphQL Wire_Name `sourceMap`."]
    pub source_map: Option<crate::IdInput<super::SourceMap>>,
    #[doc = "The value of the member in the enum\n\n`None` omits GraphQL Wire_Name `value` and preserves engine default `String(\"\")`."]
    pub value: Option<String>,
}
impl TypeDefWithEnumMemberOpts {
    #[doc = "Sets GraphQL argument `deprecated` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_deprecated(mut self, value: impl Into<String>) -> Self {
        self.deprecated = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `description` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_description(mut self, value: impl Into<String>) -> Self {
        self.description = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `sourceMap` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_source_map(mut self, value: crate::IdInput<super::SourceMap>) -> Self {
        self.source_map = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `value` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_value(mut self, value: impl Into<String>) -> Self {
        self.value = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `TypeDef.withEnumValue`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct TypeDefWithEnumValueOpts {
    #[doc = "If deprecated, the reason or migration path.\n\n`None` omits GraphQL Wire_Name `deprecated`."]
    pub deprecated: Option<String>,
    #[doc = "A doc string for the value, if any\n\n`None` omits GraphQL Wire_Name `description` and preserves engine default `String(\"\")`."]
    pub description: Option<String>,
    #[doc = "The source map for the enum value definition.\n\n`None` omits GraphQL Wire_Name `sourceMap`."]
    pub source_map: Option<crate::IdInput<super::SourceMap>>,
}
impl TypeDefWithEnumValueOpts {
    #[doc = "Sets GraphQL argument `deprecated` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_deprecated(mut self, value: impl Into<String>) -> Self {
        self.deprecated = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `description` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_description(mut self, value: impl Into<String>) -> Self {
        self.description = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `sourceMap` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_source_map(mut self, value: crate::IdInput<super::SourceMap>) -> Self {
        self.source_map = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `TypeDef.withField`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct TypeDefWithFieldOpts {
    #[doc = "If deprecated, the reason or migration path.\n\n`None` omits GraphQL Wire_Name `deprecated`."]
    pub deprecated: Option<String>,
    #[doc = "A doc string for the field, if any\n\n`None` omits GraphQL Wire_Name `description` and preserves engine default `String(\"\")`."]
    pub description: Option<String>,
    #[doc = "The source map for the field definition.\n\n`None` omits GraphQL Wire_Name `sourceMap`."]
    pub source_map: Option<crate::IdInput<super::SourceMap>>,
}
impl TypeDefWithFieldOpts {
    #[doc = "Sets GraphQL argument `deprecated` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_deprecated(mut self, value: impl Into<String>) -> Self {
        self.deprecated = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `description` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_description(mut self, value: impl Into<String>) -> Self {
        self.description = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `sourceMap` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_source_map(mut self, value: crate::IdInput<super::SourceMap>) -> Self {
        self.source_map = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `TypeDef.withInterface`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct TypeDefWithInterfaceOpts {
    #[doc = "`None` omits GraphQL Wire_Name `description` and preserves engine default `String(\"\")`."]
    pub description: Option<String>,
    #[doc = "`None` omits GraphQL Wire_Name `sourceMap`."]
    pub source_map: Option<crate::IdInput<super::SourceMap>>,
}
impl TypeDefWithInterfaceOpts {
    #[doc = "Sets GraphQL argument `description` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_description(mut self, value: impl Into<String>) -> Self {
        self.description = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `sourceMap` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_source_map(mut self, value: crate::IdInput<super::SourceMap>) -> Self {
        self.source_map = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `TypeDef.withObject`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct TypeDefWithObjectOpts {
    #[doc = "`None` omits GraphQL Wire_Name `deprecated`."]
    pub deprecated: Option<String>,
    #[doc = "`None` omits GraphQL Wire_Name `description` and preserves engine default `String(\"\")`."]
    pub description: Option<String>,
    #[doc = "`None` omits GraphQL Wire_Name `sourceMap`."]
    pub source_map: Option<crate::IdInput<super::SourceMap>>,
}
impl TypeDefWithObjectOpts {
    #[doc = "Sets GraphQL argument `deprecated` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_deprecated(mut self, value: impl Into<String>) -> Self {
        self.deprecated = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `description` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_description(mut self, value: impl Into<String>) -> Self {
        self.description = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `sourceMap` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_source_map(mut self, value: crate::IdInput<super::SourceMap>) -> Self {
        self.source_map = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `TypeDef.withScalar`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct TypeDefWithScalarOpts {
    #[doc = "`None` omits GraphQL Wire_Name `description` and preserves engine default `String(\"\")`."]
    pub description: Option<String>,
}
impl TypeDefWithScalarOpts {
    #[doc = "Sets GraphQL argument `description` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_description(mut self, value: impl Into<String>) -> Self {
        self.description = Some(value.into());
        self
    }
}
impl crate::IntoID<crate::Id> for TypeDef {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for TypeDef {
    fn graphql_type() -> &'static str {
        "TypeDef"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<TypeDef> for crate::IdInput<TypeDef> {
    fn from(value: TypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<TypeDef> for crate::IdInput<super::NodeClient> {
    fn from(value: TypeDef) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl TypeDef {
    #[doc = "If kind is ENUM, the enum-specific type definition. If kind is not ENUM, this will be null.\n\nSelects GraphQL Wire_Name `asEnum` on `TypeDef`."]
    pub async fn as_enum(&self) -> Result<Option<super::EnumTypeDef>, crate::QueryError> {
        let query = self.selection.select("asEnum");
        let query = query.select("id");
        query
            .execute_reentry::<super::EnumTypeDef, Option<crate::Id>>(&self.session, "EnumTypeDef")
            .await
    }
    #[doc = "If kind is INPUT, the input-specific type definition. If kind is not INPUT, this will be null.\n\nSelects GraphQL Wire_Name `asInput` on `TypeDef`."]
    pub async fn as_input(&self) -> Result<Option<super::InputTypeDef>, crate::QueryError> {
        let query = self.selection.select("asInput");
        let query = query.select("id");
        query
            .execute_reentry::<super::InputTypeDef, Option<crate::Id>>(
                &self.session,
                "InputTypeDef",
            )
            .await
    }
    #[doc = "If kind is INTERFACE, the interface-specific type definition. If kind is not INTERFACE, this will be null.\n\nSelects GraphQL Wire_Name `asInterface` on `TypeDef`."]
    pub async fn as_interface(&self) -> Result<Option<super::InterfaceTypeDef>, crate::QueryError> {
        let query = self.selection.select("asInterface");
        let query = query.select("id");
        query
            .execute_reentry::<super::InterfaceTypeDef, Option<crate::Id>>(
                &self.session,
                "InterfaceTypeDef",
            )
            .await
    }
    #[doc = "If kind is LIST, the list-specific type definition. If kind is not LIST, this will be null.\n\nSelects GraphQL Wire_Name `asList` on `TypeDef`."]
    pub async fn as_list(&self) -> Result<Option<super::ListTypeDef>, crate::QueryError> {
        let query = self.selection.select("asList");
        let query = query.select("id");
        query
            .execute_reentry::<super::ListTypeDef, Option<crate::Id>>(&self.session, "ListTypeDef")
            .await
    }
    #[doc = "If kind is OBJECT, the object-specific type definition. If kind is not OBJECT, this will be null.\n\nSelects GraphQL Wire_Name `asObject` on `TypeDef`."]
    pub async fn as_object(&self) -> Result<Option<super::ObjectTypeDef>, crate::QueryError> {
        let query = self.selection.select("asObject");
        let query = query.select("id");
        query
            .execute_reentry::<super::ObjectTypeDef, Option<crate::Id>>(
                &self.session,
                "ObjectTypeDef",
            )
            .await
    }
    #[doc = "If kind is SCALAR, the scalar-specific type definition. If kind is not SCALAR, this will be null.\n\nSelects GraphQL Wire_Name `asScalar` on `TypeDef`."]
    pub async fn as_scalar(&self) -> Result<Option<super::ScalarTypeDef>, crate::QueryError> {
        let query = self.selection.select("asScalar");
        let query = query.select("id");
        query
            .execute_reentry::<super::ScalarTypeDef, Option<crate::Id>>(
                &self.session,
                "ScalarTypeDef",
            )
            .await
    }
    #[doc = "A unique identifier for this TypeDef.\n\nSelects GraphQL Wire_Name `id` on `TypeDef`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The kind of type this is (e.g. primitive, list, object).\n\nSelects GraphQL Wire_Name `kind` on `TypeDef`."]
    pub async fn kind(&self) -> Result<super::TypeDefKind, crate::QueryError> {
        let query = self.selection.select("kind");
        query.execute(&self.session).await
    }
    #[doc = "The canonical non-optional name of the type.\n\nSelects GraphQL Wire_Name `name` on `TypeDef`."]
    pub async fn name(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("name");
        query.execute(&self.session).await
    }
    #[doc = "Whether this type can be set to null. Defaults to false.\n\nSelects GraphQL Wire_Name `optional` on `TypeDef`."]
    pub async fn optional(&self) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("optional");
        query.execute(&self.session).await
    }
    #[doc = "Adds a function for constructing a new instance of an Object TypeDef, failing if the type is not an object.\n\nSelects GraphQL Wire_Name `withConstructor` on `TypeDef`."]
    #[must_use]
    pub fn with_constructor(
        &self,
        function: impl Into<crate::IdInput<super::Function>>,
    ) -> super::TypeDef {
        let query = self.selection.select("withConstructor");
        let query = query.arg_id_input("function", function.into());
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns a TypeDef of kind Enum with the provided name.\n\nNote that an enum's values may be omitted if the intent is only to refer to an enum. This is how functions are able to return their own, or any other circular reference.\n\nSelects GraphQL Wire_Name `withEnum` on `TypeDef`."]
    #[must_use]
    pub fn with_enum(&self, name: impl Into<String>) -> super::TypeDef {
        let query = self.selection.select("withEnum");
        let query = query.arg("name", name.into());
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withEnum` with a borrowed, reusable `TypeDefWithEnumOpts` value."]
    #[must_use]
    pub fn with_enum_opts(
        &self,
        name: impl Into<String>,
        opts: &TypeDefWithEnumOpts,
    ) -> super::TypeDef {
        let query = self.selection.select("withEnum");
        let query = if let Some(value) = &opts.description {
            query.arg("description", value)
        } else {
            query
        };
        let query = query.arg("name", name.into());
        let query = if let Some(value) = &opts.source_map {
            query.arg_id_input("sourceMap", value.clone())
        } else {
            query
        };
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Adds a static value for an Enum TypeDef, failing if the type is not an enum.\n\nSelects GraphQL Wire_Name `withEnumMember` on `TypeDef`."]
    #[must_use]
    pub fn with_enum_member(&self, name: impl Into<String>) -> super::TypeDef {
        let query = self.selection.select("withEnumMember");
        let query = query.arg("name", name.into());
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withEnumMember` with a borrowed, reusable `TypeDefWithEnumMemberOpts` value."]
    #[must_use]
    pub fn with_enum_member_opts(
        &self,
        name: impl Into<String>,
        opts: &TypeDefWithEnumMemberOpts,
    ) -> super::TypeDef {
        let query = self.selection.select("withEnumMember");
        let query = if let Some(value) = &opts.deprecated {
            query.arg("deprecated", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.description {
            query.arg("description", value)
        } else {
            query
        };
        let query = query.arg("name", name.into());
        let query = if let Some(value) = &opts.source_map {
            query.arg_id_input("sourceMap", value.clone())
        } else {
            query
        };
        let query = if let Some(value) = &opts.value {
            query.arg("value", value)
        } else {
            query
        };
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Adds a static value for an Enum TypeDef, failing if the type is not an enum.\n\nSelects GraphQL Wire_Name `withEnumValue` on `TypeDef`.\n\n**Deprecated:** Use `withEnumMember` instead"]
    #[deprecated(note = "Use `withEnumMember` instead")]
    #[must_use]
    pub fn with_enum_value(&self, value: impl Into<String>) -> super::TypeDef {
        let query = self.selection.select("withEnumValue");
        let query = query.arg("value", value.into());
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withEnumValue` with a borrowed, reusable `TypeDefWithEnumValueOpts` value.\n\n**Deprecated:** Use `withEnumMember` instead"]
    #[deprecated(note = "Use `withEnumMember` instead")]
    #[must_use]
    pub fn with_enum_value_opts(
        &self,
        value: impl Into<String>,
        opts: &TypeDefWithEnumValueOpts,
    ) -> super::TypeDef {
        let query = self.selection.select("withEnumValue");
        let query = if let Some(value) = &opts.deprecated {
            query.arg("deprecated", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.description {
            query.arg("description", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.source_map {
            query.arg_id_input("sourceMap", value.clone())
        } else {
            query
        };
        let query = query.arg("value", value.into());
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Adds a static field for an Object TypeDef, failing if the type is not an object.\n\nSelects GraphQL Wire_Name `withField` on `TypeDef`."]
    #[must_use]
    pub fn with_field(
        &self,
        name: impl Into<String>,
        type_def: impl Into<crate::IdInput<super::TypeDef>>,
    ) -> super::TypeDef {
        let query = self.selection.select("withField");
        let query = query.arg("name", name.into());
        let query = query.arg_id_input("typeDef", type_def.into());
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withField` with a borrowed, reusable `TypeDefWithFieldOpts` value."]
    #[must_use]
    pub fn with_field_opts(
        &self,
        name: impl Into<String>,
        type_def: impl Into<crate::IdInput<super::TypeDef>>,
        opts: &TypeDefWithFieldOpts,
    ) -> super::TypeDef {
        let query = self.selection.select("withField");
        let query = if let Some(value) = &opts.deprecated {
            query.arg("deprecated", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.description {
            query.arg("description", value)
        } else {
            query
        };
        let query = query.arg("name", name.into());
        let query = if let Some(value) = &opts.source_map {
            query.arg_id_input("sourceMap", value.clone())
        } else {
            query
        };
        let query = query.arg_id_input("typeDef", type_def.into());
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Adds a function for an Object or Interface TypeDef, failing if the type is not one of those kinds.\n\nSelects GraphQL Wire_Name `withFunction` on `TypeDef`."]
    #[must_use]
    pub fn with_function(
        &self,
        function: impl Into<crate::IdInput<super::Function>>,
    ) -> super::TypeDef {
        let query = self.selection.select("withFunction");
        let query = query.arg_id_input("function", function.into());
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns a TypeDef of kind Interface with the provided name.\n\nSelects GraphQL Wire_Name `withInterface` on `TypeDef`."]
    #[must_use]
    pub fn with_interface(&self, name: impl Into<String>) -> super::TypeDef {
        let query = self.selection.select("withInterface");
        let query = query.arg("name", name.into());
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withInterface` with a borrowed, reusable `TypeDefWithInterfaceOpts` value."]
    #[must_use]
    pub fn with_interface_opts(
        &self,
        name: impl Into<String>,
        opts: &TypeDefWithInterfaceOpts,
    ) -> super::TypeDef {
        let query = self.selection.select("withInterface");
        let query = if let Some(value) = &opts.description {
            query.arg("description", value)
        } else {
            query
        };
        let query = query.arg("name", name.into());
        let query = if let Some(value) = &opts.source_map {
            query.arg_id_input("sourceMap", value.clone())
        } else {
            query
        };
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Sets the kind of the type.\n\nSelects GraphQL Wire_Name `withKind` on `TypeDef`."]
    #[must_use]
    pub fn with_kind(&self, kind: super::TypeDefKind) -> super::TypeDef {
        let query = self.selection.select("withKind");
        let query = query.arg("kind", kind);
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns a TypeDef of kind List with the provided type for its elements.\n\nSelects GraphQL Wire_Name `withListOf` on `TypeDef`."]
    #[must_use]
    pub fn with_list_of(
        &self,
        element_type: impl Into<crate::IdInput<super::TypeDef>>,
    ) -> super::TypeDef {
        let query = self.selection.select("withListOf");
        let query = query.arg_id_input("elementType", element_type.into());
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns a TypeDef of kind Object with the provided name.\n\nNote that an object's fields and functions may be omitted if the intent is only to refer to an object. This is how functions are able to return their own object, or any other circular reference.\n\nSelects GraphQL Wire_Name `withObject` on `TypeDef`."]
    #[must_use]
    pub fn with_object(&self, name: impl Into<String>) -> super::TypeDef {
        let query = self.selection.select("withObject");
        let query = query.arg("name", name.into());
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withObject` with a borrowed, reusable `TypeDefWithObjectOpts` value."]
    #[must_use]
    pub fn with_object_opts(
        &self,
        name: impl Into<String>,
        opts: &TypeDefWithObjectOpts,
    ) -> super::TypeDef {
        let query = self.selection.select("withObject");
        let query = if let Some(value) = &opts.deprecated {
            query.arg("deprecated", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.description {
            query.arg("description", value)
        } else {
            query
        };
        let query = query.arg("name", name.into());
        let query = if let Some(value) = &opts.source_map {
            query.arg_id_input("sourceMap", value.clone())
        } else {
            query
        };
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Sets whether this type can be set to null.\n\nSelects GraphQL Wire_Name `withOptional` on `TypeDef`."]
    #[must_use]
    pub fn with_optional(&self, optional: bool) -> super::TypeDef {
        let query = self.selection.select("withOptional");
        let query = query.arg("optional", optional);
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Returns a TypeDef of kind Scalar with the provided name.\n\nSelects GraphQL Wire_Name `withScalar` on `TypeDef`."]
    #[must_use]
    pub fn with_scalar(&self, name: impl Into<String>) -> super::TypeDef {
        let query = self.selection.select("withScalar");
        let query = query.arg("name", name.into());
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withScalar` with a borrowed, reusable `TypeDefWithScalarOpts` value."]
    #[must_use]
    pub fn with_scalar_opts(
        &self,
        name: impl Into<String>,
        opts: &TypeDefWithScalarOpts,
    ) -> super::TypeDef {
        let query = self.selection.select("withScalar");
        let query = if let Some(value) = &opts.description {
            query.arg("description", value)
        } else {
            query
        };
        let query = query.arg("name", name.into());
        super::TypeDef {
            session: self.session.clone(),
            selection: query,
        }
    }
}
impl super::Node for TypeDef {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
