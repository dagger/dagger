//! Source-side interpretation of the syntax shared with authoring procedural macros.
//!
//! This parser operates only on immutable source text. Rust name resolution, cfg
//! closure, target type validation, and multi-file discovery remain later compiler
//! phases; procedural macros likewise do not acquire those responsibilities.

use std::collections::BTreeMap;
use std::num::NonZeroU32;

use proc_macro2::{Delimiter, Span, TokenStream, TokenTree};
use quote::ToTokens;
use syn::parse::Parser;
use syn::spanned::Spanned;
use syn::{Attribute, Fields, FnArg, ImplItem, Item, Pat, Visibility};

use super::diagnostic::{ModuleDiagnostic, ModuleDiagnosticCode, ModuleDiagnosticSet};
use super::model::{
    AuthoringFingerprintValue, CfgEnvironment, ModuleSourcePath, RustSymbol, SourceCoordinate,
};

const FNV_OFFSET_BASIS: u128 = 0x6c62_272e_07bb_0142_62b8_2175_6295_c58d;
const FNV_PRIME: u128 = 0x0000_0000_0100_0000_0000_0000_0000_013b;

const MARKERS: &[&str] = &[
    "constructor",
    "context",
    "field",
    "function",
    "root",
    "state",
];
const VALUES: &[&str] = &[
    "cache",
    "default",
    "default_address",
    "default_path",
    "deprecated",
    "doc",
    "ignore",
    "rename",
    "role",
    "target",
];

/// Normal Rust accessibility retained independently of Dagger export intent.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AuthoringVisibility {
    /// Public outside the crate.
    Public,
    /// Visible throughout the authored crate.
    Crate,
    /// Not accessible from generated sibling modules.
    Private,
}

/// Supported explicit authoring declaration kind.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AuthoringDeclarationKind {
    /// Stateful object struct.
    Object,
    /// Interface trait.
    Interface,
    /// Unit-variant enum.
    Enum,
    /// Transparent scalar newtype.
    Scalar,
}

/// Persistence/exposure policy selected for one object field.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AuthoringFieldPolicy {
    /// Public TypeDef field and persistent state.
    Field,
    /// Private persistent state omitted from TypeDef.
    State,
    /// Ordinary implementation detail reconstructed with `Default`.
    Transient,
}

/// One normalized authored object field.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AuthoringField {
    /// Rust field name.
    pub name: String,
    /// Authored Rust type tokens.
    pub rust_type: String,
    /// Explicit persistence/exposure policy.
    pub policy: AuthoringFieldPolicy,
    /// Canonical shared metadata.
    pub metadata: BTreeMap<String, String>,
    /// Sanitized ordinary Rust documentation.
    pub documentation: Option<String>,
    /// Ordinary Rust deprecation reason, or an empty string for bare deprecation.
    pub deprecation: Option<String>,
    /// Primary authored coordinate.
    pub source: SourceCoordinate,
}

/// One normalized exported interface method.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AuthoringInterfaceMethod {
    /// Rust method name.
    pub name: String,
    /// Parameters excluding the required shared receiver.
    pub parameters: Vec<AuthoringParameter>,
    /// Authored return type tokens.
    pub output: String,
    /// Canonical shared metadata.
    pub metadata: BTreeMap<String, String>,
    /// Sanitized ordinary Rust documentation.
    pub documentation: Option<String>,
    /// Ordinary Rust deprecation reason, or an empty string for bare deprecation.
    pub deprecation: Option<String>,
    /// Primary authored coordinate.
    pub source: SourceCoordinate,
}

/// One normalized unit enum variant.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AuthoringVariant {
    /// Rust variant name.
    pub name: String,
    /// Canonical shared metadata.
    pub metadata: BTreeMap<String, String>,
    /// Sanitized ordinary Rust documentation.
    pub documentation: Option<String>,
    /// Ordinary Rust deprecation reason, or an empty string for bare deprecation.
    pub deprecation: Option<String>,
    /// Primary authored coordinate.
    pub source: SourceCoordinate,
}

/// One normalized exported function parameter.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AuthoringParameter {
    /// Rust identifier pattern.
    pub name: String,
    /// Authored Rust type tokens.
    pub rust_type: String,
    /// Canonical shared metadata.
    pub metadata: BTreeMap<String, String>,
    /// Sanitized ordinary Rust documentation.
    pub documentation: Option<String>,
    /// Ordinary Rust deprecation reason, or an empty string for bare deprecation.
    pub deprecation: Option<String>,
    /// Primary authored coordinate.
    pub source: SourceCoordinate,
}

/// One normalized constructor or function from a `methods` impl.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AuthoringFunction {
    /// Rust method name.
    pub name: String,
    /// Whether the function has an authored receiver.
    pub has_receiver: bool,
    /// Whether the function is async.
    pub is_async: bool,
    /// Authored return type tokens.
    pub output: String,
    /// Parameters excluding the receiver.
    pub parameters: Vec<AuthoringParameter>,
    /// Canonical shared metadata.
    pub metadata: BTreeMap<String, String>,
    /// Sanitized ordinary Rust documentation.
    pub documentation: Option<String>,
    /// Ordinary Rust deprecation reason, or an empty string for bare deprecation.
    pub deprecation: Option<String>,
    /// Shared-grammar fingerprint.
    pub fingerprint: AuthoringFingerprintValue,
    /// Primary authored coordinate.
    pub source: SourceCoordinate,
}

/// One explicit type export interpreted directly from Rust source.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AuthoringDeclaration {
    /// Export kind.
    pub kind: AuthoringDeclarationKind,
    /// Crate-relative symbol available at this parsing stage.
    pub rust_symbol: RustSymbol,
    /// Ordinary Rust accessibility.
    pub visibility: AuthoringVisibility,
    /// Canonical outer authoring metadata.
    pub metadata: BTreeMap<String, String>,
    /// Sanitized ordinary Rust documentation.
    pub documentation: Option<String>,
    /// Ordinary Rust deprecation reason, or an empty string for bare deprecation.
    pub deprecation: Option<String>,
    /// Object fields, empty for other declaration kinds.
    pub fields: Vec<AuthoringField>,
    /// Interface methods, empty for other declaration kinds.
    pub interface_methods: Vec<AuthoringInterfaceMethod>,
    /// Enum variants, empty for other declaration kinds.
    pub variants: Vec<AuthoringVariant>,
    /// Transparent scalar representation, absent for other declaration kinds.
    pub scalar_representation: Option<String>,
    /// Exported inherent functions attached from `methods` impls.
    pub functions: Vec<AuthoringFunction>,
    /// Shared-grammar fingerprint.
    pub fingerprint: AuthoringFingerprintValue,
    /// Primary authored coordinate.
    pub source: SourceCoordinate,
}

/// Pure source-side parser for the shared authoring ABI.
pub struct AuthoringParser;

type AuthoringResult<T> = Result<T, Box<ModuleDiagnostic>>;

impl AuthoringParser {
    /// Parses explicit exports and methods from one immutable source document.
    pub fn parse(
        path: &ModuleSourcePath,
        source: &str,
    ) -> Result<Vec<AuthoringDeclaration>, ModuleDiagnosticSet> {
        Self::parse_in_module(path, source, &[])
    }

    /// Parses one document as the body of a known crate module.
    ///
    /// External-module traversal owns the module path; accepting it explicitly keeps
    /// this parser free of filesystem conventions while retaining canonical symbols.
    pub fn parse_in_module(
        path: &ModuleSourcePath,
        source: &str,
        module_path: &[String],
    ) -> Result<Vec<AuthoringDeclaration>, ModuleDiagnosticSet> {
        let mut file = parse_file(path, source)?;
        parse_declarations_file(path, &mut file, module_path)
    }

    /// Parses only declarations enabled by the immutable explicit cfg environment.
    pub fn parse_in_module_configured(
        path: &ModuleSourcePath,
        source: &str,
        module_path: &[String],
        cfg: &CfgEnvironment,
    ) -> Result<Vec<AuthoringDeclaration>, ModuleDiagnosticSet> {
        let mut file = Self::configured_file(path, source, cfg)?;
        parse_declarations_file(path, &mut file, module_path)
    }

    /// Extracts exported inherent methods even when their type is declared elsewhere.
    ///
    /// Discovery merges these by resolved canonical parent symbol after all external
    /// modules are known, so ordinary split impl blocks do not depend on file order.
    pub fn functions_in_module(
        path: &ModuleSourcePath,
        source: &str,
        module_path: &[String],
    ) -> Result<Vec<(RustSymbol, AuthoringFunction)>, ModuleDiagnosticSet> {
        let mut file = parse_file(path, source)?;
        collect_functions_file(path, &mut file, module_path)
    }

    /// Extracts only exported methods enabled by the explicit cfg environment.
    pub fn functions_in_module_configured(
        path: &ModuleSourcePath,
        source: &str,
        module_path: &[String],
        cfg: &CfgEnvironment,
    ) -> Result<Vec<(RustSymbol, AuthoringFunction)>, ModuleDiagnosticSet> {
        let mut file = Self::configured_file(path, source, cfg)?;
        collect_functions_file(path, &mut file, module_path)
    }

    pub(super) fn configured_file(
        path: &ModuleSourcePath,
        source: &str,
        cfg: &CfgEnvironment,
    ) -> Result<syn::File, ModuleDiagnosticSet> {
        let mut file = parse_file(path, source)?;
        let mut diagnostics = Vec::new();
        retain_configured_items(path, &mut file.items, cfg, &mut diagnostics);
        if let Some(diagnostics) = ModuleDiagnosticSet::new(diagnostics) {
            Err(diagnostics)
        } else {
            Ok(file)
        }
    }
}

fn parse_file(path: &ModuleSourcePath, source: &str) -> Result<syn::File, ModuleDiagnosticSet> {
    syn::parse_file(source).map_err(|error| {
        one_diagnostic(diagnostic(
            ModuleDiagnosticCode::SourceModuleInvalid,
            Some(coordinate(path, error.span())),
            "Rust source could not be parsed",
            "repair the authored Rust syntax before module discovery",
        ))
    })
}

fn parse_declarations_file(
    path: &ModuleSourcePath,
    file: &mut syn::File,
    module_path: &[String],
) -> Result<Vec<AuthoringDeclaration>, ModuleDiagnosticSet> {
    let mut diagnostics = Vec::new();
    let mut declarations = Vec::new();
    parse_items(
        path,
        &mut file.items,
        module_path,
        &mut declarations,
        &mut diagnostics,
    );
    if let Some(diagnostics) = ModuleDiagnosticSet::new(diagnostics) {
        return Err(diagnostics);
    }
    declarations.sort_by(|left, right| left.rust_symbol.cmp(&right.rust_symbol));
    Ok(declarations)
}

fn collect_functions_file(
    path: &ModuleSourcePath,
    file: &mut syn::File,
    module_path: &[String],
) -> Result<Vec<(RustSymbol, AuthoringFunction)>, ModuleDiagnosticSet> {
    let mut functions = Vec::new();
    let mut diagnostics = Vec::new();
    collect_functions(
        path,
        &mut file.items,
        module_path,
        &mut functions,
        &mut diagnostics,
    );
    if let Some(diagnostics) = ModuleDiagnosticSet::new(diagnostics) {
        return Err(diagnostics);
    }
    functions.sort_by(|left, right| {
        left.0
            .cmp(&right.0)
            .then_with(|| left.1.name.cmp(&right.1.name))
            .then_with(|| left.1.source.cmp(&right.1.source))
    });
    Ok(functions)
}

fn retain_configured_items(
    path: &ModuleSourcePath,
    items: &mut Vec<Item>,
    cfg: &CfgEnvironment,
    diagnostics: &mut Vec<ModuleDiagnostic>,
) {
    items.retain_mut(|item| {
        let included = match super::source::cfg_enabled(item_attributes(item), cfg) {
            Ok(included) => included,
            Err(()) => {
                diagnostics.push(diagnostic(
                    ModuleDiagnosticCode::CfgUnresolved,
                    Some(coordinate(path, item.span())),
                    "an authored item depends on configuration absent from the snapshot",
                    "declare the custom cfg input explicitly or remove the dependency",
                ));
                false
            }
        };
        if !included {
            return false;
        }
        match item {
            Item::Impl(item_impl) => item_impl.items.retain(|item| match item {
                ImplItem::Fn(function) => {
                    configured_member(path, &function.attrs, function.span(), cfg, diagnostics)
                }
                _ => true,
            }),
            Item::Trait(item_trait) => item_trait.items.retain(|item| match item {
                syn::TraitItem::Fn(function) => {
                    configured_member(path, &function.attrs, function.span(), cfg, diagnostics)
                }
                _ => true,
            }),
            Item::Mod(module) => {
                if let Some((_, nested)) = &mut module.content {
                    retain_configured_items(path, nested, cfg, diagnostics);
                }
            }
            _ => {}
        }
        true
    });
}

fn configured_member(
    path: &ModuleSourcePath,
    attributes: &[Attribute],
    span: Span,
    cfg: &CfgEnvironment,
    diagnostics: &mut Vec<ModuleDiagnostic>,
) -> bool {
    match super::source::cfg_enabled(attributes, cfg) {
        Ok(included) => included,
        Err(()) => {
            diagnostics.push(diagnostic(
                ModuleDiagnosticCode::CfgUnresolved,
                Some(coordinate(path, span)),
                "an authored method depends on configuration absent from the snapshot",
                "declare the custom cfg input explicitly or remove the dependency",
            ));
            false
        }
    }
}

fn item_attributes(item: &Item) -> &[Attribute] {
    match item {
        Item::Const(item) => &item.attrs,
        Item::Enum(item) => &item.attrs,
        Item::ExternCrate(item) => &item.attrs,
        Item::Fn(item) => &item.attrs,
        Item::ForeignMod(item) => &item.attrs,
        Item::Impl(item) => &item.attrs,
        Item::Macro(item) => &item.attrs,
        Item::Mod(item) => &item.attrs,
        Item::Static(item) => &item.attrs,
        Item::Struct(item) => &item.attrs,
        Item::Trait(item) => &item.attrs,
        Item::TraitAlias(item) => &item.attrs,
        Item::Type(item) => &item.attrs,
        Item::Union(item) => &item.attrs,
        Item::Use(item) => &item.attrs,
        _ => &[],
    }
}

fn collect_functions(
    path: &ModuleSourcePath,
    items: &mut [Item],
    module_path: &[String],
    functions: &mut Vec<(RustSymbol, AuthoringFunction)>,
    diagnostics: &mut Vec<ModuleDiagnostic>,
) {
    for item in items.iter_mut() {
        if let Item::Impl(item_impl) = item {
            match parse_methods(path, item_impl) {
                Ok(Some((self_type, methods))) => {
                    let symbol = qualify_symbol(module_path, &self_type);
                    match RustSymbol::new(symbol) {
                        Ok(symbol) => functions.extend(
                            methods
                                .into_iter()
                                .map(|function| (symbol.clone(), function)),
                        ),
                        Err(_) => diagnostics.push(diagnostic(
                            ModuleDiagnosticCode::RustPathInvalid,
                            Some(coordinate(path, item_impl.self_ty.span())),
                            "an exported impl parent path is not canonical",
                            "use a concrete crate-local object path",
                        )),
                    }
                }
                Ok(None) => {}
                Err(error) => diagnostics.push(*error),
            }
        }
    }
    for item in items {
        if let Item::Mod(module) = item
            && let Some((_, nested)) = &mut module.content
        {
            let mut nested_path = module_path.to_vec();
            nested_path.push(module.ident.to_string());
            collect_functions(path, nested, &nested_path, functions, diagnostics);
        }
    }
}

fn qualify_symbol(module_path: &[String], spelling: &str) -> String {
    if spelling.starts_with("crate::") {
        return spelling.to_owned();
    }
    let mut base = module_path.to_vec();
    let mut suffix = spelling;
    if let Some(rest) = suffix.strip_prefix("self::") {
        suffix = rest;
    }
    while let Some(rest) = suffix.strip_prefix("super::") {
        base.pop();
        suffix = rest;
    }
    std::iter::once("crate")
        .chain(base.iter().map(String::as_str))
        .chain(suffix.split("::"))
        .collect::<Vec<_>>()
        .join("::")
}

fn parse_items(
    path: &ModuleSourcePath,
    items: &mut [Item],
    module_path: &[String],
    declarations: &mut Vec<AuthoringDeclaration>,
    diagnostics: &mut Vec<ModuleDiagnostic>,
) {
    let mut functions = BTreeMap::<String, Vec<AuthoringFunction>>::new();
    for item in items.iter_mut() {
        if let Item::Impl(item_impl) = item {
            match parse_methods(path, item_impl) {
                Ok(Some((self_type, methods))) => {
                    functions
                        .entry(
                            self_type
                                .rsplit("::")
                                .next()
                                .unwrap_or(&self_type)
                                .to_owned(),
                        )
                        .or_default()
                        .extend(methods);
                }
                Ok(None) => {}
                Err(error) => diagnostics.push(*error),
            }
        }
    }

    for item in items.iter_mut() {
        let result = match item {
            Item::Struct(item) => {
                if let Some((kind, outer)) = take_outer(&mut item.attrs) {
                    match kind.as_str() {
                        "object" => parse_object(path, module_path, item, outer),
                        "scalar" => parse_scalar(path, module_path, item, outer),
                        _ => Ok(None),
                    }
                } else {
                    Ok(None)
                }
            }
            Item::Trait(item) => {
                if let Some((kind, outer)) = take_outer(&mut item.attrs) {
                    if kind == "interface" {
                        parse_interface(path, module_path, item, outer)
                    } else {
                        Ok(None)
                    }
                } else {
                    Ok(None)
                }
            }
            Item::Enum(item) => {
                if let Some((kind, outer)) = take_outer(&mut item.attrs) {
                    if kind == "enum_type" {
                        parse_enum(path, module_path, item, outer)
                    } else {
                        Ok(None)
                    }
                } else {
                    Ok(None)
                }
            }
            Item::Macro(item)
                if item.attrs.iter().any(|attribute| {
                    attribute.path().segments.last().is_some_and(|segment| {
                        matches!(
                            segment.ident.to_string().as_str(),
                            "object" | "interface" | "enum_type" | "scalar" | "methods"
                        )
                    })
                }) =>
            {
                Err(boxed_diagnostic(
                    ModuleDiagnosticCode::ExplicitExportRequired,
                    Some(coordinate(path, item.span())),
                    "a marked macro invocation has uninspectable exported output",
                    "declare the exported Rust item explicitly outside macro expansion",
                ))
            }
            _ => Ok(None),
        };

        match result {
            Ok(Some(mut declaration)) => {
                let item_name = declaration
                    .rust_symbol
                    .as_str()
                    .rsplit("::")
                    .next()
                    .unwrap_or(declaration.rust_symbol.as_str())
                    .to_owned();
                declaration.functions = functions.remove(&item_name).unwrap_or_default();
                declaration.functions.sort_by(|left, right| {
                    left.name
                        .cmp(&right.name)
                        .then_with(|| left.source.cmp(&right.source))
                });
                declarations.push(declaration);
            }
            Ok(None) => {}
            Err(error) => diagnostics.push(*error),
        }
    }

    for item in items {
        if let Item::Mod(module) = item
            && let Some((_, nested)) = &mut module.content
        {
            let mut nested_path = module_path.to_vec();
            nested_path.push(module.ident.to_string());
            parse_items(path, nested, &nested_path, declarations, diagnostics);
        }
    }
}

fn parse_object(
    path: &ModuleSourcePath,
    module_path: &[String],
    item: &mut syn::ItemStruct,
    outer: AttributeInput,
) -> AuthoringResult<Option<AuthoringDeclaration>> {
    let visibility = visibility(&item.vis);
    require_accessible(path, visibility, item.ident.span())?;
    if !item.generics.params.is_empty() || item.generics.where_clause.is_some() {
        return Err(boxed_diagnostic(
            ModuleDiagnosticCode::TypeUnsupported,
            Some(coordinate(path, item.generics.span())),
            "generic Dagger objects are not supported",
            "replace the export with a concrete crate-accessible object",
        ));
    }
    let outer = parse_metadata(path, outer)?;
    let Fields::Named(fields) = &mut item.fields else {
        return Err(boxed_diagnostic(
            ModuleDiagnosticCode::StateShapeInvalid,
            Some(coordinate(path, item.fields.span())),
            "a Dagger object must use named fields",
            "use a named-field struct so generated state remains explicit",
        ));
    };

    let mut parts = vec![
        "object".to_owned(),
        item.ident.to_string(),
        outer.canonical(),
    ];
    let mut authored_fields = Vec::new();
    for field in &mut fields.named {
        let metadata = take_nested(path, &mut field.attrs)?;
        let name = field
            .ident
            .as_ref()
            .map(ToString::to_string)
            .ok_or_else(|| {
                boxed_diagnostic(
                    ModuleDiagnosticCode::StateShapeInvalid,
                    Some(coordinate(path, field.span())),
                    "an object field has no Rust identifier",
                    "use a named field",
                )
            })?;
        let policy = if metadata.has("field") {
            AuthoringFieldPolicy::Field
        } else if metadata.has("state") {
            AuthoringFieldPolicy::State
        } else {
            AuthoringFieldPolicy::Transient
        };
        let policy_name = match policy {
            AuthoringFieldPolicy::Field => "field",
            AuthoringFieldPolicy::State => "state",
            AuthoringFieldPolicy::Transient => "transient",
        };
        let rust_type = canonical_tokens(&field.ty);
        parts.extend([
            policy_name.to_owned(),
            name.clone(),
            fingerprint_tokens(&field.ty),
            metadata.canonical(),
        ]);
        authored_fields.push(AuthoringField {
            name,
            rust_type,
            policy,
            metadata: metadata.values,
            documentation: rustdoc(&field.attrs),
            deprecation: rust_deprecation(&field.attrs),
            source: coordinate(path, field.span()),
        });
    }
    Ok(Some(declaration(
        path,
        module_path,
        item.ident.span(),
        &item.ident,
        DeclarationParts {
            kind: AuthoringDeclarationKind::Object,
            visibility,
            metadata: outer.values,
            documentation: rustdoc(&item.attrs),
            deprecation: rust_deprecation(&item.attrs),
            fields: authored_fields,
            interface_methods: Vec::new(),
            variants: Vec::new(),
            scalar_representation: None,
            fingerprint: fingerprint(parts),
        },
    )))
}

fn parse_interface(
    path: &ModuleSourcePath,
    module_path: &[String],
    item: &mut syn::ItemTrait,
    outer: AttributeInput,
) -> AuthoringResult<Option<AuthoringDeclaration>> {
    let visibility = visibility(&item.vis);
    require_accessible(path, visibility, item.ident.span())?;
    let outer = parse_metadata(path, outer)?;
    if !item.generics.params.is_empty()
        || item.generics.where_clause.is_some()
        || !item.supertraits.is_empty()
    {
        return Err(boxed_diagnostic(
            ModuleDiagnosticCode::InterfaceInvalid,
            Some(coordinate(path, item.generics.span())),
            "an exported interface uses unsupported generics or supertraits",
            "use one concrete trait without associated type parameters",
        ));
    }
    let mut parts = vec![
        "interface".to_owned(),
        item.ident.to_string(),
        outer.canonical(),
    ];
    let mut interface_methods = Vec::new();
    for trait_item in &mut item.items {
        let syn::TraitItem::Fn(function) = trait_item else {
            return Err(boxed_diagnostic(
                ModuleDiagnosticCode::InterfaceInvalid,
                Some(coordinate(path, trait_item.span())),
                "an exported interface contains an unsupported associated item",
                "retain only supported receiver methods in the interface",
            ));
        };
        let metadata = take_nested(path, &mut function.attrs)?;
        let parameter_metadata = take_parameter_metadata(path, &mut function.sig.inputs)?;
        validate_interface_signature(path, function, &parameter_metadata)?;
        let mut parameters = Vec::new();
        for (argument, metadata) in function.sig.inputs.iter().zip(&parameter_metadata) {
            let FnArg::Typed(argument) = argument else {
                continue;
            };
            let Pat::Ident(ident) = argument.pat.as_ref() else {
                return Err(boxed_diagnostic(
                    ModuleDiagnosticCode::InterfaceInvalid,
                    Some(coordinate(path, argument.pat.span())),
                    "an interface parameter must use an identifier pattern",
                    "replace the pattern with a named typed parameter",
                ));
            };
            parameters.push(AuthoringParameter {
                name: ident.ident.to_string(),
                rust_type: canonical_tokens(&argument.ty),
                metadata: metadata.values.clone(),
                documentation: rustdoc(&argument.attrs),
                deprecation: rust_deprecation(&argument.attrs),
                source: coordinate(path, ident.ident.span()),
            });
        }
        parts.extend([
            function.sig.ident.to_string(),
            fingerprint_tokens(&function.sig),
            metadata.canonical(),
        ]);
        interface_methods.push(AuthoringInterfaceMethod {
            name: function.sig.ident.to_string(),
            parameters,
            output: canonical_tokens(&function.sig.output),
            metadata: metadata.values,
            documentation: rustdoc(&function.attrs),
            deprecation: rust_deprecation(&function.attrs),
            source: coordinate(path, function.sig.ident.span()),
        });
    }
    Ok(Some(declaration(
        path,
        module_path,
        item.ident.span(),
        &item.ident,
        DeclarationParts {
            kind: AuthoringDeclarationKind::Interface,
            visibility,
            metadata: outer.values,
            documentation: rustdoc(&item.attrs),
            deprecation: rust_deprecation(&item.attrs),
            fields: Vec::new(),
            interface_methods,
            variants: Vec::new(),
            scalar_representation: None,
            fingerprint: fingerprint(parts),
        },
    )))
}

fn parse_enum(
    path: &ModuleSourcePath,
    module_path: &[String],
    item: &mut syn::ItemEnum,
    outer: AttributeInput,
) -> AuthoringResult<Option<AuthoringDeclaration>> {
    let visibility = visibility(&item.vis);
    require_accessible(path, visibility, item.ident.span())?;
    let outer = parse_metadata(path, outer)?;
    let mut parts = vec!["enum".to_owned(), item.ident.to_string(), outer.canonical()];
    let mut variants = Vec::new();
    for variant in &mut item.variants {
        if !matches!(variant.fields, Fields::Unit) {
            return Err(boxed_diagnostic(
                ModuleDiagnosticCode::EnumInvalid,
                Some(coordinate(path, variant.fields.span())),
                "a Dagger enum supports only unit variants",
                "replace payload variants with objects or unit variants",
            ));
        }
        let metadata = take_nested(path, &mut variant.attrs)?;
        parts.extend([variant.ident.to_string(), metadata.canonical()]);
        variants.push(AuthoringVariant {
            name: variant.ident.to_string(),
            metadata: metadata.values,
            documentation: rustdoc(&variant.attrs),
            deprecation: rust_deprecation(&variant.attrs),
            source: coordinate(path, variant.ident.span()),
        });
    }
    Ok(Some(declaration(
        path,
        module_path,
        item.ident.span(),
        &item.ident,
        DeclarationParts {
            kind: AuthoringDeclarationKind::Enum,
            visibility,
            metadata: outer.values,
            documentation: rustdoc(&item.attrs),
            deprecation: rust_deprecation(&item.attrs),
            fields: Vec::new(),
            interface_methods: Vec::new(),
            variants,
            scalar_representation: None,
            fingerprint: fingerprint(parts),
        },
    )))
}

fn parse_scalar(
    path: &ModuleSourcePath,
    module_path: &[String],
    item: &mut syn::ItemStruct,
    outer: AttributeInput,
) -> AuthoringResult<Option<AuthoringDeclaration>> {
    let visibility = visibility(&item.vis);
    require_accessible(path, visibility, item.ident.span())?;
    let outer = parse_metadata(path, outer)?;
    let Fields::Unnamed(fields) = &mut item.fields else {
        return Err(boxed_diagnostic(
            ModuleDiagnosticCode::ScalarInvalid,
            Some(coordinate(path, item.fields.span())),
            "a Dagger scalar must be a transparent one-field tuple struct",
            "use a tuple newtype over one supported scalar representation",
        ));
    };
    if fields.unnamed.len() != 1 {
        return Err(boxed_diagnostic(
            ModuleDiagnosticCode::ScalarInvalid,
            Some(coordinate(path, fields.span())),
            "a Dagger scalar must contain exactly one field",
            "use one transparent scalar representation",
        ));
    }
    let field = &mut fields.unnamed[0];
    let metadata = take_nested(path, &mut field.attrs)?;
    let representation = canonical_tokens(&field.ty);
    let parts = [
        "scalar".to_owned(),
        item.ident.to_string(),
        outer.canonical(),
        fingerprint_tokens(&field.ty),
        metadata.canonical(),
    ];
    Ok(Some(declaration(
        path,
        module_path,
        item.ident.span(),
        &item.ident,
        DeclarationParts {
            kind: AuthoringDeclarationKind::Scalar,
            visibility,
            metadata: outer.values,
            documentation: rustdoc(&item.attrs),
            deprecation: rust_deprecation(&item.attrs),
            fields: Vec::new(),
            interface_methods: Vec::new(),
            variants: Vec::new(),
            scalar_representation: Some(representation),
            fingerprint: fingerprint(parts),
        },
    )))
}

fn validate_interface_signature(
    path: &ModuleSourcePath,
    function: &syn::TraitItemFn,
    parameter_metadata: &[Metadata],
) -> AuthoringResult<()> {
    let signature = &function.sig;
    if !signature.generics.params.is_empty()
        || signature.generics.where_clause.is_some()
        || signature.constness.is_some()
        || signature.asyncness.is_some()
        || signature.unsafety.is_some()
        || signature.abi.is_some()
        || signature.variadic.is_some()
    {
        return Err(boxed_diagnostic(
            ModuleDiagnosticCode::InterfaceInvalid,
            Some(coordinate(path, signature.span())),
            "an interface method uses unsupported generics or qualifiers",
            "use one concrete safe receiver method",
        ));
    }
    let Some(receiver) = signature.receiver() else {
        return Err(boxed_diagnostic(
            ModuleDiagnosticCode::InterfaceInvalid,
            Some(coordinate(path, signature.span())),
            "an interface method has no shared receiver",
            "declare the method with `&self`",
        ));
    };
    if receiver.reference.is_none()
        || receiver.mutability.is_some()
        || receiver.colon_token.is_some()
        || parameter_metadata
            .first()
            .is_some_and(|metadata| !metadata.values.is_empty())
        || parameter_metadata
            .iter()
            .skip(1)
            .any(|metadata| metadata.has("context"))
    {
        return Err(boxed_diagnostic(
            ModuleDiagnosticCode::InterfaceInvalid,
            Some(coordinate(path, receiver.span())),
            "an interface method receiver or injected context is unsupported",
            "use `&self` and ordinary typed data parameters",
        ));
    }
    Ok(())
}

fn parse_methods(
    path: &ModuleSourcePath,
    item: &mut syn::ItemImpl,
) -> AuthoringResult<Option<(String, Vec<AuthoringFunction>)>> {
    let Some((kind, args)) = take_outer(&mut item.attrs) else {
        return Ok(None);
    };
    if kind != "methods" {
        return Ok(None);
    }
    let _outer = parse_metadata(path, args)?;
    if item.trait_.is_some() {
        return Ok(None);
    }
    let self_name = match item.self_ty.as_ref() {
        syn::Type::Path(type_path) if type_path.qself.is_none() => type_path
            .path
            .segments
            .iter()
            .map(|segment| segment.ident.to_string())
            .collect::<Vec<_>>()
            .join("::"),
        _ => return Err(invalid_self_type(path, item.self_ty.span())),
    };
    let mut methods = Vec::new();
    for impl_item in &mut item.items {
        let ImplItem::Fn(function) = impl_item else {
            continue;
        };
        let metadata = take_nested(path, &mut function.attrs)?;
        let parameter_metadata = take_parameter_metadata(path, &mut function.sig.inputs)?;
        if !metadata.has("constructor") && !metadata.has("function") {
            if parameter_metadata
                .iter()
                .any(|metadata| !metadata.values.is_empty())
            {
                return Err(boxed_diagnostic(
                    ModuleDiagnosticCode::MetadataConflict,
                    Some(coordinate(path, function.sig.span())),
                    "Dagger parameter metadata requires an exported function",
                    "mark the function explicitly or remove its parameter metadata",
                ));
            }
            continue;
        }
        validate_export_signature(path, function, &metadata, &parameter_metadata)?;

        let mut parameters = Vec::new();
        for (argument, metadata) in function.sig.inputs.iter().zip(&parameter_metadata) {
            let FnArg::Typed(argument) = argument else {
                continue;
            };
            let Pat::Ident(ident) = argument.pat.as_ref() else {
                return Err(boxed_diagnostic(
                    ModuleDiagnosticCode::FunctionSignatureInvalid,
                    Some(coordinate(path, argument.pat.span())),
                    "an exported parameter must use an identifier pattern",
                    "replace the pattern with a named typed parameter",
                ));
            };
            parameters.push(AuthoringParameter {
                name: ident.ident.to_string(),
                rust_type: canonical_tokens(&argument.ty),
                metadata: metadata.values.clone(),
                documentation: rustdoc(&argument.attrs),
                deprecation: rust_deprecation(&argument.attrs),
                source: coordinate(path, ident.ident.span()),
            });
        }

        let mut parts = vec![
            "method".to_owned(),
            fingerprint_tokens(item.self_ty.as_ref()),
            function.sig.ident.to_string(),
            fingerprint_tokens(&function.sig),
            metadata.canonical(),
        ];
        parts.extend(parameter_metadata.iter().map(Metadata::canonical));
        methods.push(AuthoringFunction {
            name: function.sig.ident.to_string(),
            has_receiver: function.sig.receiver().is_some(),
            is_async: function.sig.asyncness.is_some(),
            output: canonical_tokens(&function.sig.output),
            parameters,
            metadata: metadata.values,
            documentation: rustdoc(&function.attrs),
            deprecation: rust_deprecation(&function.attrs),
            fingerprint: fingerprint(parts),
            source: coordinate(path, function.sig.ident.span()),
        });
    }
    methods.sort_by(|left, right| left.name.cmp(&right.name));
    Ok(Some((self_name, methods)))
}

fn validate_export_signature(
    path: &ModuleSourcePath,
    function: &syn::ImplItemFn,
    metadata: &Metadata,
    parameter_metadata: &[Metadata],
) -> AuthoringResult<()> {
    let signature = &function.sig;
    let unsupported_qualifier = signature.constness.is_some()
        || signature.unsafety.is_some()
        || signature.abi.is_some()
        || signature.variadic.is_some();
    if !signature.generics.params.is_empty()
        || signature.generics.where_clause.is_some()
        || unsupported_qualifier
    {
        return Err(boxed_diagnostic(
            ModuleDiagnosticCode::FunctionSignatureInvalid,
            Some(coordinate(path, signature.span())),
            "an exported function uses unsupported generics or qualifiers",
            "use one concrete safe Rust function signature",
        ));
    }

    if let Some(receiver) = signature.receiver() {
        let shared_reference = receiver.reference.is_some()
            && receiver.mutability.is_none()
            && receiver.colon_token.is_none();
        if !shared_reference || metadata.has("constructor") {
            return Err(boxed_diagnostic(
                ModuleDiagnosticCode::FunctionSignatureInvalid,
                Some(coordinate(path, receiver.span())),
                "an exported receiver is unsupported for this function",
                "use `&self` for an instance function or no receiver for a constructor",
            ));
        }
    }

    if signature.receiver().is_some_and(|_| {
        parameter_metadata
            .first()
            .is_some_and(|metadata| !metadata.values.is_empty())
    }) {
        return Err(boxed_diagnostic(
            ModuleDiagnosticCode::FunctionMetadataInvalid,
            Some(coordinate(path, signature.span())),
            "receiver metadata cannot be projected as a Dagger argument",
            "move supported metadata to a named data parameter",
        ));
    }
    Ok(())
}

fn rustdoc(attributes: &[Attribute]) -> Option<String> {
    let lines = attributes
        .iter()
        .filter(|attribute| attribute.path().is_ident("doc"))
        .filter_map(|attribute| match &attribute.meta {
            syn::Meta::NameValue(value) => match &value.value {
                syn::Expr::Lit(literal) => match &literal.lit {
                    syn::Lit::Str(value) => Some(value.value()),
                    _ => None,
                },
                _ => None,
            },
            _ => None,
        })
        .map(|line| line.trim().to_owned())
        .collect::<Vec<_>>();
    let documentation = lines.join("\n").trim().to_owned();
    (!documentation.is_empty()).then_some(documentation)
}

fn rust_deprecation(attributes: &[Attribute]) -> Option<String> {
    attributes
        .iter()
        .find(|attribute| attribute.path().is_ident("deprecated"))
        .map(|attribute| match &attribute.meta {
            syn::Meta::Path(_) => String::new(),
            syn::Meta::NameValue(value) => match &value.value {
                syn::Expr::Lit(literal) => match &literal.lit {
                    syn::Lit::Str(value) => value.value(),
                    _ => String::new(),
                },
                _ => String::new(),
            },
            syn::Meta::List(list) => {
                let mut reason = None;
                let _ = list.parse_nested_meta(|nested| {
                    if nested.path.is_ident("note") {
                        reason = Some(nested.value()?.parse::<syn::LitStr>()?.value());
                    } else if nested.input.peek(syn::Token![=]) {
                        let _ = nested.value()?.parse::<syn::Expr>()?;
                    }
                    Ok(())
                });
                reason.unwrap_or_default()
            }
        })
}

struct DeclarationParts {
    kind: AuthoringDeclarationKind,
    visibility: AuthoringVisibility,
    metadata: BTreeMap<String, String>,
    documentation: Option<String>,
    deprecation: Option<String>,
    fields: Vec<AuthoringField>,
    interface_methods: Vec<AuthoringInterfaceMethod>,
    variants: Vec<AuthoringVariant>,
    scalar_representation: Option<String>,
    fingerprint: AuthoringFingerprintValue,
}

fn declaration(
    path: &ModuleSourcePath,
    module_path: &[String],
    span: Span,
    ident: &syn::Ident,
    parts: DeclarationParts,
) -> AuthoringDeclaration {
    let symbol = std::iter::once("crate".to_owned())
        .chain(module_path.iter().cloned())
        .chain(std::iter::once(ident.to_string()))
        .collect::<Vec<_>>()
        .join("::");
    AuthoringDeclaration {
        kind: parts.kind,
        rust_symbol: RustSymbol::new(symbol)
            .expect("a parsed Rust identifier forms a valid crate-relative symbol"),
        visibility: parts.visibility,
        metadata: parts.metadata,
        documentation: parts.documentation,
        deprecation: parts.deprecation,
        fields: parts.fields,
        interface_methods: parts.interface_methods,
        variants: parts.variants,
        scalar_representation: parts.scalar_representation,
        functions: Vec::new(),
        fingerprint: parts.fingerprint,
        source: coordinate(path, span),
    }
}

#[derive(Clone)]
struct AttributeInput {
    tokens: TokenStream,
    span: Span,
    malformed: bool,
}

fn take_outer(attributes: &mut Vec<Attribute>) -> Option<(String, AttributeInput)> {
    let index = attributes.iter().position(|attribute| {
        attribute.path().segments.last().is_some_and(|segment| {
            matches!(
                segment.ident.to_string().as_str(),
                "object" | "interface" | "enum_type" | "scalar" | "methods"
            )
        })
    })?;
    let attribute = attributes.remove(index);
    let kind = attribute.path().segments.last()?.ident.to_string();
    let span = attribute.span();
    let (tokens, malformed) = match attribute.meta {
        syn::Meta::Path(_) => (TokenStream::new(), false),
        syn::Meta::List(list) => (list.tokens, false),
        syn::Meta::NameValue(_) => (TokenStream::new(), true),
    };
    Some((
        kind,
        AttributeInput {
            tokens,
            span,
            malformed,
        },
    ))
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
struct Metadata {
    values: BTreeMap<String, String>,
    fingerprint_values: BTreeMap<String, String>,
}

impl Metadata {
    fn parse(input: AttributeInput) -> Result<Self, syn::Error> {
        if input.malformed {
            return Err(syn::Error::new(
                input.span,
                "Dagger authoring attributes require list or marker syntax",
            ));
        }
        let mut metadata = Self::default();
        let parser = syn::meta::parser(|nested| metadata.parse_nested(nested));
        parser.parse2(input.tokens)?;
        Ok(metadata)
    }

    fn parse_nested(&mut self, nested: syn::meta::ParseNestedMeta<'_>) -> syn::Result<()> {
        let Some(ident) = nested.path.get_ident() else {
            return Err(nested.error("Dagger metadata names must be single identifiers"));
        };
        let name = ident.to_string();
        if self.values.contains_key(&name) {
            return Err(nested.error(format!("duplicate Dagger metadata `{name}`")));
        }
        for (left, right) in [("field", "state"), ("constructor", "function")] {
            if (name == left && self.has(right)) || (name == right && self.has(left)) {
                return Err(
                    nested.error(format!("Dagger metadata `{left}` conflicts with `{right}`"))
                );
            }
        }
        let context_compatible = matches!(name.as_str(), "context" | "doc" | "deprecated");
        if (name == "context"
            && self
                .values
                .keys()
                .any(|existing| !matches!(existing.as_str(), "context" | "doc" | "deprecated")))
            || (self.has("context") && !context_compatible)
        {
            return Err(nested.error("a Dagger context parameter cannot carry value metadata"));
        }
        let (value, fingerprint_value) = if MARKERS.contains(&name.as_str()) {
            if nested.input.peek(syn::Token![=]) || nested.input.peek(syn::token::Paren) {
                return Err(nested.error(format!("Dagger marker `{name}` accepts no value")));
            }
            ("true".to_owned(), "true".to_owned())
        } else if VALUES.contains(&name.as_str()) {
            if nested.input.peek(syn::Token![=]) {
                let value = nested.value()?.parse::<syn::Expr>()?;
                (canonical_tokens(&value), fingerprint_tokens(&value))
            } else if nested.input.peek(syn::token::Paren) {
                let content;
                syn::parenthesized!(content in nested.input);
                let value = content.parse::<TokenStream>()?;
                (canonical_tokens(&value), fingerprint_tokens(&value))
            } else {
                return Err(nested.error(format!("Dagger metadata `{name}` requires a value")));
            }
        } else {
            return Err(nested.error(format!("unknown Dagger metadata `{name}`")));
        };
        self.values.insert(name.clone(), value);
        self.fingerprint_values.insert(name, fingerprint_value);
        Ok(())
    }

    fn has(&self, name: &str) -> bool {
        self.values.contains_key(name)
    }

    fn canonical(&self) -> String {
        self.fingerprint_values
            .iter()
            .map(|(name, value)| format!("{}:{name}={value}", name.len()))
            .collect::<Vec<_>>()
            .join("|")
    }
}

fn parse_metadata(path: &ModuleSourcePath, input: AttributeInput) -> AuthoringResult<Metadata> {
    Metadata::parse(input.clone()).map_err(|error| metadata_diagnostic(path, error.span(), &error))
}

fn take_nested(
    path: &ModuleSourcePath,
    attributes: &mut Vec<Attribute>,
) -> AuthoringResult<Metadata> {
    let mut metadata = Metadata::default();
    let mut retained = Vec::with_capacity(attributes.len());
    for attribute in attributes.drain(..) {
        if attribute.path().is_ident("dagger") {
            if let Err(error) = attribute.parse_nested_meta(|nested| metadata.parse_nested(nested))
            {
                return Err(metadata_diagnostic(path, error.span(), &error));
            }
        } else {
            retained.push(attribute);
        }
    }
    *attributes = retained;
    Ok(metadata)
}

fn take_parameter_metadata(
    path: &ModuleSourcePath,
    inputs: &mut syn::punctuated::Punctuated<FnArg, syn::Token![,]>,
) -> AuthoringResult<Vec<Metadata>> {
    inputs
        .iter_mut()
        .map(|input| match input {
            FnArg::Receiver(receiver) => take_nested(path, &mut receiver.attrs),
            FnArg::Typed(argument) => take_nested(path, &mut argument.attrs),
        })
        .collect()
}

fn visibility(visibility: &Visibility) -> AuthoringVisibility {
    match visibility {
        Visibility::Public(_) => AuthoringVisibility::Public,
        Visibility::Restricted(restricted) if restricted.path.is_ident("crate") => {
            AuthoringVisibility::Crate
        }
        Visibility::Inherited | Visibility::Restricted(_) => AuthoringVisibility::Private,
    }
}

fn require_accessible(
    path: &ModuleSourcePath,
    visibility: AuthoringVisibility,
    span: Span,
) -> AuthoringResult<()> {
    if visibility == AuthoringVisibility::Private {
        Err(boxed_diagnostic(
            ModuleDiagnosticCode::ExportVisibilityInvalid,
            Some(coordinate(path, span)),
            "an exported type is inaccessible to generated sibling code",
            "make the type `pub(crate)` or `pub` without changing member privacy",
        ))
    } else {
        Ok(())
    }
}

fn invalid_self_type(path: &ModuleSourcePath, span: Span) -> Box<ModuleDiagnostic> {
    boxed_diagnostic(
        ModuleDiagnosticCode::FunctionSignatureInvalid,
        Some(coordinate(path, span)),
        "the exported impl self type is unsupported",
        "use a concrete local object type",
    )
}

fn metadata_diagnostic(
    path: &ModuleSourcePath,
    span: Span,
    error: &syn::Error,
) -> Box<ModuleDiagnostic> {
    let text = error.to_string();
    let code = if text.contains("unknown") {
        ModuleDiagnosticCode::MetadataUnknown
    } else if text.contains("duplicate") || text.contains("conflict") {
        ModuleDiagnosticCode::MetadataConflict
    } else {
        ModuleDiagnosticCode::MetadataMalformed
    };
    boxed_diagnostic(
        code,
        Some(coordinate(path, span)),
        "Dagger authoring metadata is invalid",
        "use one supported, non-conflicting metadata item",
    )
}

fn diagnostic(
    code: ModuleDiagnosticCode,
    source: Option<SourceCoordinate>,
    message: &'static str,
    remediation: &'static str,
) -> ModuleDiagnostic {
    ModuleDiagnostic::new(code, source, message, remediation)
        .expect("static compiler diagnostics satisfy the safe renderer policy")
}

fn boxed_diagnostic(
    code: ModuleDiagnosticCode,
    source: Option<SourceCoordinate>,
    message: &'static str,
    remediation: &'static str,
) -> Box<ModuleDiagnostic> {
    Box::new(diagnostic(code, source, message, remediation))
}

fn one_diagnostic(diagnostic: ModuleDiagnostic) -> ModuleDiagnosticSet {
    ModuleDiagnosticSet::new([diagnostic])
        .expect("a single compiler diagnostic is a non-empty diagnostic set")
}

fn coordinate(path: &ModuleSourcePath, span: Span) -> SourceCoordinate {
    let start = span.start();
    SourceCoordinate {
        path: path.clone(),
        line: NonZeroU32::new(u32::try_from(start.line).unwrap_or(u32::MAX).max(1))
            .expect("the coordinate is clamped to a non-zero line"),
        column: NonZeroU32::new(
            u32::try_from(start.column.saturating_add(1))
                .unwrap_or(u32::MAX)
                .max(1),
        )
        .expect("the coordinate is clamped to a non-zero column"),
    }
}

fn canonical_tokens(tokens: &impl ToTokens) -> String {
    tokens.to_token_stream().to_string()
}

fn fingerprint_tokens(tokens: &impl ToTokens) -> String {
    let mut canonical = String::new();
    append_tokens(tokens.to_token_stream(), &mut canonical);
    canonical
}

fn append_tokens(tokens: TokenStream, canonical: &mut String) {
    for token in tokens {
        match token {
            TokenTree::Group(group) => {
                canonical.push('g');
                canonical.push(match group.delimiter() {
                    Delimiter::Parenthesis => 'p',
                    Delimiter::Brace => 'b',
                    Delimiter::Bracket => 's',
                    Delimiter::None => 'n',
                });
                append_tokens(group.stream(), canonical);
                canonical.push('e');
            }
            TokenTree::Ident(ident) => append_framed('i', &ident.to_string(), canonical),
            TokenTree::Punct(punct) => {
                canonical.push('p');
                canonical.push(punct.as_char());
            }
            TokenTree::Literal(literal) => append_framed('l', &literal.to_string(), canonical),
        }
    }
}

fn append_framed(kind: char, value: &str, canonical: &mut String) {
    canonical.push(kind);
    canonical.push_str(&value.len().to_string());
    canonical.push(':');
    canonical.push_str(value);
}

fn fingerprint(parts: impl IntoIterator<Item = String>) -> AuthoringFingerprintValue {
    let mut value = FNV_OFFSET_BASIS;
    for part in parts {
        let length = u64::try_from(part.len()).unwrap_or(u64::MAX);
        for byte in length.to_le_bytes().into_iter().chain(part.bytes()) {
            value ^= u128::from(byte);
            value = value.wrapping_mul(FNV_PRIME);
        }
    }
    AuthoringFingerprintValue::from_u128(value)
}
