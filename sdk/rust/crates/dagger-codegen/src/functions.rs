use std::{collections::HashSet, ops::Deref, sync::Arc};

use dagger_sdk::core::introspection::{
    DirectivesExt, FullType, FullTypeFields, InputValue, TypeRef, __TypeKind,
};
use itertools::Itertools;

use crate::utility::OptionExt;

pub trait FormatTypeFuncs {
    fn format_kind_list(&self, representation: &str, input: bool, immutable: bool) -> String;
    fn format_kind_scalar_string(&self, representation: &str, input: bool) -> String;
    fn format_kind_scalar_int(&self, representation: &str) -> String;
    fn format_kind_scalar_float(&self, representation: &str) -> String;
    fn format_kind_scalar_boolean(&self, representation: &str) -> String;
    fn format_kind_scalar_default(
        &self,
        representation: &str,
        ref_name: &str,
        input: bool,
    ) -> String;
    fn format_kind_object(&self, representation: &str, ref_name: &str) -> String;
    fn format_kind_interface(&self, representation: &str, ref_name: &str) -> String;
    fn format_kind_input_object(&self, representation: &str, ref_name: &str) -> String;
    fn format_kind_enum(&self, representation: &str, ref_name: &str) -> String;
}

pub type DynFormatTypeFuncs = Arc<dyn FormatTypeFuncs + Send + Sync>;

pub struct CommonFunctions {
    format_type_funcs: DynFormatTypeFuncs,
    supports_nullable_objects: bool,
    supports_id_handles: bool,
    interface_names: HashSet<String>,
}

impl CommonFunctions {
    pub fn new(
        funcs: DynFormatTypeFuncs,
        schema_version: Option<&str>,
        interface_names: HashSet<String>,
    ) -> Self {
        Self {
            format_type_funcs: funcs,
            supports_nullable_objects: supports_nullable_objects(schema_version),
            supports_id_handles: supports_id_handles(schema_version),
            interface_names,
        }
    }

    pub fn supports_nullable_objects(&self) -> bool {
        self.supports_nullable_objects
    }

    /// Whether the named schema type is an interface, whose handles load
    /// into its `FooClient` struct.
    pub fn is_interface(&self, name: &str) -> bool {
        self.interface_names.contains(name)
    }

    pub fn format_input_type(&self, t: &TypeRef) -> String {
        self.format_type(t, true, false)
    }

    pub fn format_output_type(&self, t: &TypeRef) -> String {
        self.format_type(t, false, false)
    }

    pub fn format_immutable_input_type(&self, t: &TypeRef) -> String {
        self.format_type(t, true, true)
    }

    /// Returns true if the field returns an ID that should be converted into
    /// an object: see `id_handle_type`. This mirrors Go's `ConvertID`.
    pub fn convert_id(&self, field: &FullTypeFields) -> bool {
        self.id_handle_type(field).is_some()
    }

    /// The GraphQL type an ID-returning field loads in the SDK, or `None`
    /// when the ID is returned as-is (including the `id` field itself).
    ///
    /// The `@expectedType` directive names the object. Fields returning
    /// their parent's own ID (sync-likes) have always been loaded as the
    /// parent, and from the ID handle cutover on every expected type is
    /// loaded, so `LLM.spawn` returns an `Agent` rather than its ID. Older
    /// views keep the parent-only rule so their signatures don't move.
    pub fn id_handle_type(&self, field: &FullTypeFields) -> Option<String> {
        // Never convert the `id` field itself.
        if field.name.as_deref() == Some("id") {
            return None;
        }
        let type_ref = &field.type_.as_ref()?.type_ref;
        // Must be the ID scalar (after unwrapping NON_NULL).
        if !type_ref.is_scalar() || !type_ref.is_id() {
            return None;
        }
        let expected = field.directives.expected_type()?;
        let parent_name = field
            .parent_type
            .as_ref()
            .and_then(|p| p.name.as_deref())
            .unwrap_or_default();
        if expected == parent_name || self.supports_id_handles {
            Some(expected)
        } else {
            None
        }
    }

    fn format_type(&self, t: &TypeRef, input: bool, immutable: bool) -> String {
        let mut representation = String::new();
        let mut r = Some(t.clone());
        while r.is_some() {
            return match r.as_ref() {
                Some(rf) => match rf.kind.as_ref() {
                    Some(k) => match k {
                        __TypeKind::SCALAR => match Scalar::from(rf) {
                            Scalar::Int => self
                                .format_type_funcs
                                .format_kind_scalar_int(&representation),
                            Scalar::Float => self
                                .format_type_funcs
                                .format_kind_scalar_float(&representation),
                            Scalar::String => {
                                if immutable {
                                    "&'a str".into()
                                } else {
                                    self.format_type_funcs
                                        .format_kind_scalar_string(&representation, input)
                                }
                            }
                            Scalar::Boolean => self
                                .format_type_funcs
                                .format_kind_scalar_boolean(&representation),
                            Scalar::Default => self.format_type_funcs.format_kind_scalar_default(
                                &representation,
                                rf.name.as_ref().unwrap(),
                                input,
                            ),
                        },
                        __TypeKind::OBJECT => self
                            .format_type_funcs
                            .format_kind_object(&representation, rf.name.as_ref().unwrap()),
                        __TypeKind::ENUM => self
                            .format_type_funcs
                            .format_kind_enum(&representation, rf.name.as_ref().unwrap()),
                        __TypeKind::INPUT_OBJECT => self
                            .format_type_funcs
                            .format_kind_input_object(&representation, rf.name.as_ref().unwrap()),
                        __TypeKind::LIST => {
                            if let Some(rf) = &rf.of_type {
                                let inner_type = self.format_type(rf, input, immutable);

                                representation = self.format_type_funcs.format_kind_list(
                                    &inner_type,
                                    input,
                                    immutable,
                                );

                                return representation;
                            } else {
                                continue;
                            }
                        }
                        __TypeKind::NON_NULL => {
                            r = get_type(rf);
                            continue;
                        }
                        __TypeKind::Other(_) => {
                            r = get_type(rf);
                            continue;
                        }
                        __TypeKind::INTERFACE => self
                            .format_type_funcs
                            .format_kind_interface(&representation, rf.name.as_ref().unwrap()),
                        __TypeKind::UNION => break,
                    },
                    None => break,
                },
                None => break,
            };
        }

        representation
    }
}

fn supports_nullable_objects(schema_version: Option<&str>) -> bool {
    schema_version_at_least(schema_version, [1, 0, 0], ("beta", 10))
}

/// Whether every `@expectedType` ID return is loaded as its object.
fn supports_id_handles(schema_version: Option<&str>) -> bool {
    schema_version_at_least(schema_version, [1, 0, 0], ("beta", 12))
}

/// Compare a schema version against a feature's beta cutover. Unknown or
/// non-semver versions (development builds) get every feature; a beta
/// prerelease is compared by its beta number, ignoring any dev suffix.
fn schema_version_at_least(
    schema_version: Option<&str>,
    cutover_core: [u64; 3],
    cutover_prerelease: (&str, u64),
) -> bool {
    let Some(version) = schema_version.filter(|version| !version.is_empty()) else {
        return true;
    };
    let version = version.trim_start_matches('v');
    let (core, prerelease) = match version.split_once('-') {
        Some((core, prerelease)) => (core, Some(prerelease)),
        None => (version, None),
    };
    let Ok(core) = core
        .split('.')
        .take(3)
        .map(|part| part.parse::<u64>())
        .collect::<Result<Vec<_>, _>>()
    else {
        return true;
    };
    if core.as_slice() != cutover_core {
        return core.as_slice() > &cutover_core;
    }
    // A release of the cutover's core version has every feature.
    let Some(prerelease) = prerelease else {
        return true;
    };
    let (cutover_tag, cutover_number) = cutover_prerelease;
    prerelease
        .strip_prefix(cutover_tag)
        .and_then(|rest| rest.strip_prefix('.'))
        .and_then(|number| number.split(|c: char| !c.is_ascii_digit()).next())
        .and_then(|number| number.parse::<u64>().ok())
        .map(|number| number >= cutover_number)
        .unwrap_or(prerelease > cutover_tag)
}

#[cfg(test)]
mod tests {
    use super::supports_nullable_objects;

    #[test]
    fn nullable_object_version_gate_handles_boundaries_and_development_versions() {
        for (version, expected) in [
            (None, true),
            (Some(""), true),
            (Some("development"), true),
            (Some("v0.21.0-dev"), false),
            (Some("v1.0.0-beta.9-dev"), false),
            (Some("v1.0.0-beta.10"), true),
            (Some("v1.0.0-beta.10-dev"), true),
            (Some("v1.0.0-rc.1"), true),
            (Some("v1.0.0"), true),
        ] {
            assert_eq!(supports_nullable_objects(version), expected, "{version:?}");
        }
    }
}

fn get_type(type_ref: &TypeRef) -> Option<TypeRef> {
    type_ref.of_type.clone().map(|typ| *typ)
}

pub enum Scalar {
    Int,
    Float,
    String,
    Boolean,
    Default,
}

impl From<&TypeRef> for Scalar {
    fn from(value: &TypeRef) -> Self {
        match value.name.as_deref() {
            Some("Int") => Scalar::Int,
            Some("Float") => Scalar::Float,
            Some("String") => Scalar::String,
            Some("Boolean") => Scalar::Boolean,
            Some(_) => Scalar::Default,
            None => Scalar::Default,
        }
    }
}

#[allow(dead_code)]
pub fn get_type_from_name<'t>(types: &'t [FullType], name: &'t str) -> Option<&'t FullType> {
    types
        .iter()
        .find(|t| matches!(&t.name, Some(type_name) if type_name == name))
}

pub fn type_field_has_optional(field: &FullTypeFields) -> bool {
    field
        .args
        .pipe(|a| {
            a.iter()
                .filter_map(|a| a.pipe(|a| &a.input_value))
                .collect::<Vec<_>>()
        })
        .pipe(|s| s.has_optionals())
        .unwrap_or(false)
}

pub trait TypeRefExt {
    fn get_non_null(&self) -> &TypeRef;
    fn get_list_item(&self) -> &TypeRef;

    fn is_object(&self) -> bool;
    fn is_list_of_objects(&self) -> bool;
    fn is_id(&self) -> bool;
    fn is_list(&self) -> bool;
    fn is_scalar(&self) -> bool;
    fn is_optional(&self) -> bool;

    fn is_kind(&self, kind: __TypeKind) -> Option<bool>;
    fn is_kind_or_default(&self, kind: __TypeKind) -> bool;
}

impl TypeRefExt for TypeRef {
    fn get_non_null(&self) -> &TypeRef {
        unwrap_inner(self, __TypeKind::NON_NULL)
    }

    fn get_list_item(&self) -> &TypeRef {
        unwrap_inner(self, __TypeKind::LIST)
    }

    fn is_object(&self) -> bool {
        let inner = self.get_non_null();
        inner.is_kind_or_default(__TypeKind::OBJECT)
            || inner.is_kind_or_default(__TypeKind::INTERFACE)
    }

    fn is_list_of_objects(&self) -> bool {
        self.get_non_null().get_list_item().is_object()
    }

    fn is_id(&self) -> bool {
        self.get_non_null()
            .name
            .as_ref()
            .map(|n| n.ends_with("ID"))
            .unwrap_or(false)
    }

    fn is_list(&self) -> bool {
        self.get_non_null().is_kind_or_default(__TypeKind::LIST)
    }

    fn is_scalar(&self) -> bool {
        self.get_non_null().is_kind_or_default(__TypeKind::SCALAR)
    }
    fn is_optional(&self) -> bool {
        self.is_kind(__TypeKind::NON_NULL)
            .map(|opt| !opt)
            .unwrap_or(false)
    }

    fn is_kind(&self, kind: __TypeKind) -> Option<bool> {
        Some(self.kind.as_ref()? == &kind)
    }
    fn is_kind_or_default(&self, kind: __TypeKind) -> bool {
        self.is_kind(kind).unwrap_or(false)
    }
}

fn unwrap_inner(type_ref: &TypeRef, kind: __TypeKind) -> &TypeRef {
    match &type_ref.kind {
        Some(actual_kind) if actual_kind == &kind => type_ref.of_type.as_ref().unwrap().deref(),
        _ => type_ref,
    }
}

pub trait InputValuesExt {
    fn has_optionals(&self) -> bool;
}

impl InputValuesExt for Vec<&InputValue> {
    fn has_optionals(&self) -> bool {
        !self
            .iter()
            .map(|k| k.type_.is_optional())
            .filter(|t| *t)
            .collect_vec()
            .is_empty()
    }
}

impl InputValuesExt for Vec<InputValue> {
    fn has_optionals(&self) -> bool {
        !self
            .iter()
            .map(|k| k.type_.is_optional())
            .filter(|t| *t)
            .collect_vec()
            .is_empty()
    }
}

#[cfg(test)]
mod test {
    use dagger_sdk::core::introspection::{FullType, InputValue, TypeRef, __TypeKind};
    use pretty_assertions::assert_eq;

    use crate::functions::{InputValuesExt, TypeRefExt};

    use super::get_type_from_name;

    #[test]
    fn get_type_from_name_has_no_item() {
        let input = vec![];
        let output = get_type_from_name(&input, "some-name");

        assert_eq!(output.is_none(), true);
    }

    #[test]
    fn get_type_from_name_has_item() {
        let name = "some-name";
        let input = vec![FullType {
            kind: None,
            name: Some(name.to_string()),
            description: None,
            fields: None,
            input_fields: None,
            interfaces: None,
            enum_values: None,
            possible_types: None,
        }];
        let output = get_type_from_name(&input, name);

        assert_eq!(output.is_some(), true);
    }

    #[test]
    fn get_type_from_name_has_item_multiple_entries() {
        let name = "some-name";
        let input = vec![
            FullType {
                kind: None,
                name: Some(name.to_string()),
                description: None,
                fields: None,
                input_fields: None,
                interfaces: None,
                enum_values: None,
                possible_types: None,
            },
            FullType {
                kind: None,
                name: Some(name.to_string()),
                description: None,
                fields: None,
                input_fields: None,
                interfaces: None,
                enum_values: None,
                possible_types: None,
            },
        ];
        let output = get_type_from_name(&input, name);

        assert_eq!(output.is_some(), true);
    }

    #[test]
    fn type_ref_is_optional_has_none() {
        let input = TypeRef {
            kind: None,
            name: None,
            of_type: None,
        };
        let output = input.is_optional();

        assert_eq!(output, false);
    }

    #[test]
    fn type_ref_is_optional_is_required() {
        let input = TypeRef {
            kind: Some(__TypeKind::NON_NULL),
            name: None,
            of_type: None,
        };
        let output = input.is_optional();

        assert_eq!(output, false);
    }

    #[test]
    fn type_ref_is_optional_is_optional() {
        let input = TypeRef {
            kind: Some(__TypeKind::OBJECT),
            name: None,
            of_type: None,
        };
        let output = input.is_optional();

        assert_eq!(output, true);
    }

    #[test]
    fn input_values_has_optionals_none() {
        let input: Vec<InputValue> = vec![];

        let output = input.has_optionals();

        assert_eq!(output, false);
    }

    #[test]
    fn input_values_has_optionals_has_optional() {
        let input = vec![
            InputValue {
                name: "some-name".to_string(),
                description: None,
                type_: TypeRef {
                    kind: Some(__TypeKind::NON_NULL),
                    name: None,
                    of_type: None,
                },
                default_value: None,
                directives: None,
            },
            InputValue {
                name: "some-other-name".to_string(),
                description: None,
                type_: TypeRef {
                    kind: Some(__TypeKind::OBJECT),
                    name: None,
                    of_type: None,
                },
                default_value: None,
                directives: None,
            },
        ];

        let output = input.has_optionals();

        assert_eq!(output, true);
    }

    #[test]
    fn input_values_has_optionals_is_required() {
        let input = vec![
            InputValue {
                name: "some-name".to_string(),
                description: None,
                type_: TypeRef {
                    kind: Some(__TypeKind::NON_NULL),
                    name: None,
                    of_type: None,
                },
                default_value: None,
                directives: None,
            },
            InputValue {
                name: "some-other-name".to_string(),
                description: None,
                type_: TypeRef {
                    kind: Some(__TypeKind::NON_NULL),
                    name: None,
                    of_type: None,
                },
                default_value: None,
                directives: None,
            },
        ];

        let output = input.has_optionals();

        assert_eq!(output, false);
    }
}
