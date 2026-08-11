//! Source-side interpretation of the syntax shared with authoring procedural macros.
//!
//! This parser operates only on immutable source text. Rust name resolution, cfg
//! closure, target type validation, and multi-file discovery remain later compiler
//! phases; procedural macros likewise do not acquire those responsibilities.

use std::collections::BTreeMap;
use std::num::NonZeroU32;

use proc_macro2::{Span, TokenStream};
use quote::ToTokens;
use syn::parse::Parser;
use syn::spanned::Spanned;
use syn::{Attribute, Fields, FnArg, ImplItem, Item, Pat, Visibility};

use super::diagnostic::{ModuleDiagnostic, ModuleDiagnosticCode, ModuleDiagnosticSet};
use super::model::{AuthoringFingerprintValue, ModuleSourcePath, RustSymbol, SourceCoordinate};

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
    /// Object fields, empty for other declaration kinds.
    pub fields: Vec<AuthoringField>,
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
        let mut file = syn::parse_file(source).map_err(|error| {
            one_diagnostic(diagnostic(
                ModuleDiagnosticCode::SourceModuleInvalid,
                Some(coordinate(path, error.span())),
                "Rust source could not be parsed",
                "repair the authored Rust syntax before module discovery",
            ))
        })?;

        let mut declarations = Vec::new();
        let mut diagnostics = Vec::new();
        let mut functions = BTreeMap::<String, Vec<AuthoringFunction>>::new();

        for item in &mut file.items {
            if let Item::Impl(item_impl) = item {
                match parse_methods(path, item_impl) {
                    Ok(Some((self_type, methods))) => {
                        functions.entry(self_type).or_default().extend(methods);
                    }
                    Ok(None) => {}
                    Err(error) => diagnostics.push(*error),
                }
            }
        }

        for item in &mut file.items {
            let result = match item {
                Item::Struct(item) => {
                    if let Some((kind, outer)) = take_outer(&mut item.attrs) {
                        match kind.as_str() {
                            "object" => parse_object(path, item, outer),
                            "scalar" => parse_scalar(path, item, outer),
                            _ => Ok(None),
                        }
                    } else {
                        Ok(None)
                    }
                }
                Item::Trait(item) => {
                    if let Some((kind, outer)) = take_outer(&mut item.attrs) {
                        if kind == "interface" {
                            parse_interface(path, item, outer)
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
                            parse_enum(path, item, outer)
                        } else {
                            Ok(None)
                        }
                    } else {
                        Ok(None)
                    }
                }
                _ => Ok(None),
            };

            match result {
                Ok(Some(mut declaration)) => {
                    let item_name = declaration
                        .rust_symbol
                        .as_str()
                        .strip_prefix("crate::")
                        .unwrap_or(declaration.rust_symbol.as_str())
                        .to_owned();
                    declaration.functions = functions.remove(&item_name).unwrap_or_default();
                    declarations.push(declaration);
                }
                Ok(None) => {}
                Err(error) => diagnostics.push(*error),
            }
        }

        if let Some(diagnostics) = ModuleDiagnosticSet::new(diagnostics) {
            return Err(diagnostics);
        }
        declarations.sort_by(|left, right| left.rust_symbol.cmp(&right.rust_symbol));
        Ok(declarations)
    }
}

fn parse_object(
    path: &ModuleSourcePath,
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
            rust_type.clone(),
            metadata.canonical(),
        ]);
        authored_fields.push(AuthoringField {
            name,
            rust_type,
            policy,
            metadata: metadata.values,
        });
    }
    Ok(Some(declaration(
        path,
        item.ident.span(),
        &item.ident,
        DeclarationParts {
            kind: AuthoringDeclarationKind::Object,
            visibility,
            metadata: outer.values,
            fields: authored_fields,
            fingerprint: fingerprint(parts),
        },
    )))
}

fn parse_interface(
    path: &ModuleSourcePath,
    item: &mut syn::ItemTrait,
    outer: AttributeInput,
) -> AuthoringResult<Option<AuthoringDeclaration>> {
    let visibility = visibility(&item.vis);
    require_accessible(path, visibility, item.ident.span())?;
    let outer = parse_metadata(path, outer)?;
    let mut parts = vec![
        "interface".to_owned(),
        item.ident.to_string(),
        outer.canonical(),
    ];
    for trait_item in &mut item.items {
        if let syn::TraitItem::Fn(function) = trait_item {
            let metadata = take_nested(path, &mut function.attrs)?;
            let _ = take_parameter_metadata(path, &mut function.sig.inputs)?;
            parts.extend([
                function.sig.ident.to_string(),
                canonical_tokens(&function.sig),
                metadata.canonical(),
            ]);
        }
    }
    Ok(Some(declaration(
        path,
        item.ident.span(),
        &item.ident,
        DeclarationParts {
            kind: AuthoringDeclarationKind::Interface,
            visibility,
            metadata: outer.values,
            fields: Vec::new(),
            fingerprint: fingerprint(parts),
        },
    )))
}

fn parse_enum(
    path: &ModuleSourcePath,
    item: &mut syn::ItemEnum,
    outer: AttributeInput,
) -> AuthoringResult<Option<AuthoringDeclaration>> {
    let visibility = visibility(&item.vis);
    require_accessible(path, visibility, item.ident.span())?;
    let outer = parse_metadata(path, outer)?;
    let mut parts = vec!["enum".to_owned(), item.ident.to_string(), outer.canonical()];
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
    }
    Ok(Some(declaration(
        path,
        item.ident.span(),
        &item.ident,
        DeclarationParts {
            kind: AuthoringDeclarationKind::Enum,
            visibility,
            metadata: outer.values,
            fields: Vec::new(),
            fingerprint: fingerprint(parts),
        },
    )))
}

fn parse_scalar(
    path: &ModuleSourcePath,
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
    let parts = [
        "scalar".to_owned(),
        item.ident.to_string(),
        outer.canonical(),
        canonical_tokens(&field.ty),
        metadata.canonical(),
    ];
    Ok(Some(declaration(
        path,
        item.ident.span(),
        &item.ident,
        DeclarationParts {
            kind: AuthoringDeclarationKind::Scalar,
            visibility,
            metadata: outer.values,
            fields: Vec::new(),
            fingerprint: fingerprint(parts),
        },
    )))
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
        return Err(boxed_diagnostic(
            ModuleDiagnosticCode::FunctionSignatureInvalid,
            Some(coordinate(path, item.impl_token.span)),
            "the Dagger methods attribute requires an inherent impl",
            "move exported functions to an inherent impl block",
        ));
    }
    let self_type = canonical_tokens(item.self_ty.as_ref());
    let self_name = match item.self_ty.as_ref() {
        syn::Type::Path(type_path) => type_path
            .path
            .segments
            .last()
            .map(|segment| segment.ident.to_string())
            .ok_or_else(|| invalid_self_type(path, item.self_ty.span()))?,
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
            });
        }

        let mut parts = vec![
            "method".to_owned(),
            self_type.clone(),
            function.sig.ident.to_string(),
            canonical_tokens(&function.sig),
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
            fingerprint: fingerprint(parts),
            source: coordinate(path, function.sig.ident.span()),
        });
    }
    methods.sort_by(|left, right| left.name.cmp(&right.name));
    Ok(Some((self_name, methods)))
}

struct DeclarationParts {
    kind: AuthoringDeclarationKind,
    visibility: AuthoringVisibility,
    metadata: BTreeMap<String, String>,
    fields: Vec<AuthoringField>,
    fingerprint: AuthoringFingerprintValue,
}

fn declaration(
    path: &ModuleSourcePath,
    span: Span,
    ident: &syn::Ident,
    parts: DeclarationParts,
) -> AuthoringDeclaration {
    AuthoringDeclaration {
        kind: parts.kind,
        rust_symbol: RustSymbol::new(format!("crate::{ident}"))
            .expect("a parsed Rust identifier forms a valid crate-relative symbol"),
        visibility: parts.visibility,
        metadata: parts.metadata,
        fields: parts.fields,
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
        let value = if MARKERS.contains(&name.as_str()) {
            if nested.input.peek(syn::Token![=]) || nested.input.peek(syn::token::Paren) {
                return Err(nested.error(format!("Dagger marker `{name}` accepts no value")));
            }
            "true".to_owned()
        } else if VALUES.contains(&name.as_str()) {
            if nested.input.peek(syn::Token![=]) {
                canonical_tokens(&nested.value()?.parse::<syn::Expr>()?)
            } else if nested.input.peek(syn::token::Paren) {
                let content;
                syn::parenthesized!(content in nested.input);
                canonical_tokens(&content.parse::<TokenStream>()?)
            } else {
                return Err(nested.error(format!("Dagger metadata `{name}` requires a value")));
            }
        } else {
            return Err(nested.error(format!("unknown Dagger metadata `{name}`")));
        };
        self.values.insert(name, value);
        Ok(())
    }

    fn has(&self, name: &str) -> bool {
        self.values.contains_key(name)
    }

    fn canonical(&self) -> String {
        self.values
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
