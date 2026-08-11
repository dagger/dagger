//! Typed function, argument, and default-value compilation for Rust modules.
//!
//! This phase consumes normalized source declarations and the closed module type
//! catalog. It never evaluates authored expressions: supported defaults are decoded
//! from syntax into the same typed values used by runtime codecs, then stored as
//! canonical JSON.

use std::collections::BTreeMap;

use convert_case::{Case, Casing};
use quote::ToTokens;
use serde_json::{Number, Value};
use syn::{Expr, GenericArgument, PathArguments, Type};

use super::authoring::{AuthoringFunction, AuthoringParameter};
use super::diagnostic::{ModuleDiagnostic, ModuleDiagnosticCode, ModuleDiagnosticSet};
use super::model::{RustSymbol, SourceCoordinate, WireName};
use super::types::{
    ModuleValue, ModuleValueCodec, RustModuleType, TypeCatalog, TypePosition, TypeResolver,
};

const MAX_CACHE_TTL_SECONDS: u64 = 7 * 24 * 60 * 60;

/// Whether generated dispatch calls or directly awaits the authored bridge.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ExecutionKind {
    /// Direct synchronous invocation.
    Synchronous,
    /// Direct await on the active call task.
    Asynchronous,
}

/// Supported authored receiver model.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ReceiverKind {
    /// Associated function without a receiver.
    None,
    /// Shared immutable instance receiver.
    Shared,
}

/// Exported function role in the local module surface.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum FunctionKind {
    /// Root construction entrypoint.
    Constructor,
    /// Ordinary exported module function.
    Function,
}

/// Successful target result plus the authored fallible boundary, when present.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum FunctionReturn {
    /// Infallible value or target Void.
    Value(RustModuleType),
    /// Result whose error is converted by the declaring macro bridge.
    Fallible {
        /// Type projected to the target function result.
        ok: RustModuleType,
        /// Canonical authored Rust error type spelling.
        error: String,
    },
}

impl FunctionReturn {
    /// Borrows the only successful type visible to TypeDef projection.
    #[must_use]
    pub const fn success(&self) -> &RustModuleType {
        match self {
            Self::Value(value) | Self::Fallible { ok: value, .. } => value,
        }
    }
}

/// Exact target cache behavior.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum CachePolicy {
    /// Engine default caching without an explicit TTL.
    Default,
    /// Never cache this function.
    Never,
    /// Cache only within the active session.
    PerSession,
    /// Default caching with a validated whole-second TTL.
    TimeToLive(u64),
}

/// Target function role.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum FunctionRole {
    /// Ordinary module function.
    Ordinary,
    /// Check function.
    Check,
    /// Generator function.
    Generator,
    /// Up function.
    Up,
}

/// Complete metadata for one projected data argument.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ArgumentMetadata {
    /// Sanitized semantic documentation.
    pub documentation: Option<String>,
    /// Deprecation reason; an empty string represents bare deprecation.
    pub deprecation: Option<String>,
    /// Canonical JSON default accepted by the exact runtime codec.
    pub default: Option<Value>,
    /// Target default-path value.
    pub default_path: Option<String>,
    /// Target default-address value.
    pub default_address: Option<String>,
    /// Ordered target ignore patterns.
    pub ignore: Vec<String>,
    /// Whether omission has explicit semantics without erasing the Rust value type.
    pub optional: bool,
    /// Most specific authored coordinate.
    pub source: SourceCoordinate,
}

/// Complete metadata for one projected function.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FunctionMetadata {
    /// Sanitized semantic documentation.
    pub documentation: Option<String>,
    /// Exact target cache policy.
    pub cache: CachePolicy,
    /// Exact target function role.
    pub role: FunctionRole,
    /// Deprecation reason; an empty string represents bare deprecation.
    pub deprecation: Option<String>,
    /// Most specific authored coordinate.
    pub source: SourceCoordinate,
}

/// One projected data argument after context injection has been removed.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CompiledArgument {
    /// Authored Rust identifier.
    pub rust_name: String,
    /// Exact normalized or explicitly renamed wire identifier.
    pub wire_name: WireName,
    /// Closed recursive Rust/target type.
    pub ty: RustModuleType,
    /// Typed target metadata.
    pub metadata: ArgumentMetadata,
}

/// One complete exported function shape independent of dispatch execution.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CompiledFunction {
    /// Authored Rust identifier.
    pub rust_name: String,
    /// Exact normalized or explicitly renamed wire identifier.
    pub wire_name: WireName,
    /// Constructor or ordinary function.
    pub kind: FunctionKind,
    /// Supported receiver classification.
    pub receiver: ReceiverKind,
    /// Sync or async bridge selection.
    pub execution: ExecutionKind,
    /// Whether generated dispatch injects the active module context.
    pub inject_context: bool,
    /// Every data argument in authored order.
    pub arguments: Vec<CompiledArgument>,
    /// Successful target result and optional error boundary.
    pub return_type: FunctionReturn,
    /// Typed target metadata.
    pub metadata: FunctionMetadata,
}

/// Pure compiler from normalized authoring declarations to typed function shapes.
pub struct FunctionCompiler<'a> {
    resolver: TypeResolver<'a>,
    catalog: &'a TypeCatalog,
}

impl<'a> FunctionCompiler<'a> {
    /// Creates a compiler over one immutable discovery graph and type catalog.
    #[must_use]
    pub const fn new(resolver: TypeResolver<'a>, catalog: &'a TypeCatalog) -> Self {
        Self { resolver, catalog }
    }

    /// Compiles a merged impl surface and reports every colliding function coordinate.
    pub fn compile_all(
        &self,
        parent: &RustSymbol,
        root: &RustSymbol,
        functions: &[AuthoringFunction],
    ) -> Result<Vec<CompiledFunction>, ModuleDiagnosticSet> {
        let mut diagnostics = Vec::new();
        let mut compiled = Vec::new();
        for function in functions {
            match self.compile(parent, root, function) {
                Ok(function) => compiled.push(function),
                Err(error) => diagnostics.push(error),
            }
        }

        let mut by_wire = BTreeMap::<WireName, Vec<SourceCoordinate>>::new();
        for function in &compiled {
            by_wire
                .entry(function.wire_name.clone())
                .or_default()
                .push(function.metadata.source.clone());
        }
        for coordinates in by_wire.values().filter(|coordinates| coordinates.len() > 1) {
            diagnostics.extend(coordinates.iter().cloned().map(|source| {
                diagnostic(
                    ModuleDiagnosticCode::WireNameCollision,
                    Some(source),
                    "multiple functions normalize to one wire name",
                    "give each conflicting function a unique explicit rename",
                )
            }));
        }
        if diagnostics.is_empty() {
            compiled.sort_by(|left, right| {
                left.wire_name
                    .cmp(&right.wire_name)
                    .then_with(|| left.rust_name.cmp(&right.rust_name))
            });
            Ok(compiled)
        } else {
            Err(ModuleDiagnosticSet::new(diagnostics)
                .expect("function compilation collected at least one diagnostic"))
        }
    }

    /// Compiles one function and rejects invalid target combinations before rendering.
    pub fn compile(
        &self,
        parent: &RustSymbol,
        root: &RustSymbol,
        function: &AuthoringFunction,
    ) -> Result<CompiledFunction, ModuleDiagnostic> {
        let kind = if function.metadata.contains_key("constructor") {
            FunctionKind::Constructor
        } else {
            FunctionKind::Function
        };
        let receiver = if function.has_receiver {
            ReceiverKind::Shared
        } else {
            ReceiverKind::None
        };
        if kind == FunctionKind::Constructor && (parent != root || receiver != ReceiverKind::None) {
            return Err(signature_error(
                Some(function.source.clone()),
                "a constructor must be an associated function on the module root",
            ));
        }

        let mut inject_context = false;
        let mut arguments = Vec::new();
        let mut seen = BTreeMap::<WireName, SourceCoordinate>::new();
        for parameter in &function.parameters {
            if parameter.metadata.contains_key("context") {
                if inject_context
                    || final_type_segment(&parameter.rust_type) != Some("ModuleContext")
                {
                    return Err(signature_error(
                        Some(parameter.source.clone()),
                        "an injected context must be the sole marked generated ModuleContext parameter",
                    ));
                }
                inject_context = true;
                continue;
            }

            let ty = self.resolver.resolve(
                &parameter.rust_type,
                TypePosition::Input,
                Some(parameter.source.clone()),
            )?;
            let wire_name = wire_name(
                parameter.metadata.get("rename"),
                &parameter.name,
                &parameter.source,
            )?;
            if let Some(first) = seen.insert(wire_name.clone(), parameter.source.clone()) {
                return Err(diagnostic(
                    ModuleDiagnosticCode::WireNameCollision,
                    Some(first),
                    "multiple arguments normalize to one wire name",
                    "give each conflicting argument a unique explicit rename",
                ));
            }
            let metadata = self.argument_metadata(parameter, &ty)?;
            arguments.push(CompiledArgument {
                rust_name: parameter.name.clone(),
                wire_name,
                ty,
                metadata,
            });
        }

        let return_type = self.function_return(function)?;
        if kind == FunctionKind::Constructor
            && return_type.success() != &RustModuleType::LocalObject(root.clone())
        {
            return Err(diagnostic(
                ModuleDiagnosticCode::ConstructorInvalid,
                Some(function.source.clone()),
                "a constructor must return the exact module root object",
                "return the root directly or through Result",
            ));
        }
        let metadata = function_metadata(function, kind)?;
        let wire_name = wire_name(
            function.metadata.get("rename"),
            &function.name,
            &function.source,
        )?;

        Ok(CompiledFunction {
            rust_name: function.name.clone(),
            wire_name,
            kind,
            receiver,
            execution: if function.is_async {
                ExecutionKind::Asynchronous
            } else {
                ExecutionKind::Synchronous
            },
            inject_context,
            arguments,
            return_type,
            metadata,
        })
    }

    fn function_return(
        &self,
        function: &AuthoringFunction,
    ) -> Result<FunctionReturn, ModuleDiagnostic> {
        let output = function.output.trim();
        if output.is_empty() {
            return Ok(FunctionReturn::Value(RustModuleType::Void));
        }
        let spelling = output.strip_prefix("->").map(str::trim).unwrap_or(output);
        let ty = syn::parse_str::<Type>(spelling).map_err(|_| {
            signature_error(
                Some(function.source.clone()),
                "an exported return type could not be parsed",
            )
        })?;
        if is_result(&ty) && result_types(&ty).is_none() {
            return Err(signature_error(
                Some(function.source.clone()),
                "Result returns require exactly one success and one error type",
            ));
        }
        if let Some((ok, error)) = result_types(&ty) {
            let ok = self.resolver.resolve(
                &ok.to_token_stream().to_string(),
                TypePosition::Output,
                Some(function.source.clone()),
            )?;
            if !matches!(error, Type::Path(path) if path.qself.is_none()) {
                return Err(signature_error(
                    Some(function.source.clone()),
                    "a fallible export requires one concrete error type",
                ));
            }
            return Ok(FunctionReturn::Fallible {
                ok,
                error: error.to_token_stream().to_string(),
            });
        }
        self.resolver
            .resolve(
                spelling,
                TypePosition::Output,
                Some(function.source.clone()),
            )
            .map(FunctionReturn::Value)
    }

    fn argument_metadata(
        &self,
        parameter: &AuthoringParameter,
        ty: &RustModuleType,
    ) -> Result<ArgumentMetadata, ModuleDiagnostic> {
        let documentation = merged_string_metadata(
            parameter.documentation.as_ref(),
            parameter.metadata.get("doc"),
            &parameter.source,
            "documentation",
        )?;
        let deprecation = merged_string_metadata(
            parameter.deprecation.as_ref(),
            parameter.metadata.get("deprecated"),
            &parameter.source,
            "deprecation",
        )?;
        let default_path = optional_string(&parameter.metadata, "default_path", &parameter.source)?;
        let default_address =
            optional_string(&parameter.metadata, "default_address", &parameter.source)?;
        if default_path.is_some() && default_address.is_some() {
            return Err(metadata_error(
                Some(parameter.source.clone()),
                "an argument cannot have both a default path and default address",
            ));
        }
        let ignore = optional_strings(&parameter.metadata, "ignore", &parameter.source)?;
        if !ignore.is_empty() && default_address.is_some() {
            return Err(metadata_error(
                Some(parameter.source.clone()),
                "ignore patterns are incompatible with a default address",
            ));
        }
        let default = parameter
            .metadata
            .get("default")
            .map(|expression| self.typed_default(expression, ty, &parameter.source))
            .transpose()?;
        if default.is_some() && (default_path.is_some() || default_address.is_some()) {
            return Err(metadata_error(
                Some(parameter.source.clone()),
                "a typed default cannot be combined with a path or address default",
            ));
        }
        let optional = matches!(ty, RustModuleType::Optional(_))
            || default.is_some()
            || default_path.is_some()
            || default_address.is_some();
        if deprecation.is_some() && !optional {
            return Err(metadata_error(
                Some(parameter.source.clone()),
                "a required argument cannot be deprecated by the target",
            ));
        }
        Ok(ArgumentMetadata {
            documentation,
            deprecation,
            default,
            default_path,
            default_address,
            ignore,
            optional,
            source: parameter.source.clone(),
        })
    }

    fn typed_default(
        &self,
        spelling: &str,
        ty: &RustModuleType,
        source: &SourceCoordinate,
    ) -> Result<Value, ModuleDiagnostic> {
        let expression = syn::parse_str::<Expr>(spelling).map_err(|_| {
            default_error(
                Some(source.clone()),
                "a typed default expression could not be parsed",
            )
        })?;
        let value = default_value(&expression, ty, self.catalog, source)?;
        let codec = ModuleValueCodec::new(self.catalog);
        let canonical = codec.encode(ty, &value).map_err(|_| {
            default_error(
                Some(source.clone()),
                "a typed default does not match its declared argument type",
            )
        })?;
        let decoded = codec.decode(ty, &canonical).map_err(|_| {
            default_error(
                Some(source.clone()),
                "a typed default is not accepted by the runtime input codec",
            )
        })?;
        if decoded != value {
            return Err(default_error(
                Some(source.clone()),
                "a typed default is not canonical under the runtime input codec",
            ));
        }
        Ok(canonical)
    }
}

fn function_metadata(
    function: &AuthoringFunction,
    kind: FunctionKind,
) -> Result<FunctionMetadata, ModuleDiagnostic> {
    let documentation = merged_string_metadata(
        function.documentation.as_ref(),
        function.metadata.get("doc"),
        &function.source,
        "documentation",
    )?;
    let deprecation = merged_string_metadata(
        function.deprecation.as_ref(),
        function.metadata.get("deprecated"),
        &function.source,
        "deprecation",
    )?;
    let cache = parse_cache(function.metadata.get("cache"), &function.source)?;
    let role = parse_role(function.metadata.get("role"), &function.source)?;
    if kind == FunctionKind::Constructor
        && (cache != CachePolicy::Default || role != FunctionRole::Ordinary)
    {
        return Err(metadata_error(
            Some(function.source.clone()),
            "constructor cache and function-role metadata is target-incompatible",
        ));
    }
    Ok(FunctionMetadata {
        documentation,
        cache,
        role,
        deprecation,
        source: function.source.clone(),
    })
}

fn result_types(ty: &Type) -> Option<(&Type, &Type)> {
    let Type::Path(path) = ty else {
        return None;
    };
    let segment = path.path.segments.last()?;
    if segment.ident != "Result" {
        return None;
    }
    let PathArguments::AngleBracketed(arguments) = &segment.arguments else {
        return None;
    };
    let types = arguments
        .args
        .iter()
        .filter_map(|argument| match argument {
            GenericArgument::Type(ty) => Some(ty),
            _ => None,
        })
        .collect::<Vec<_>>();
    (types.len() == 2).then_some((types[0], types[1]))
}

fn is_result(ty: &Type) -> bool {
    matches!(
        ty,
        Type::Path(path)
            if path.path.segments.last().is_some_and(|segment| segment.ident == "Result")
    )
}

fn default_value(
    expression: &Expr,
    ty: &RustModuleType,
    catalog: &TypeCatalog,
    source: &SourceCoordinate,
) -> Result<ModuleValue, ModuleDiagnostic> {
    match (expression, ty) {
        (Expr::Lit(expression), RustModuleType::String) => match &expression.lit {
            syn::Lit::Str(value) => Ok(ModuleValue::String(value.value())),
            _ => Err(default_kind(source)),
        },
        (Expr::Lit(expression), RustModuleType::Boolean) => match &expression.lit {
            syn::Lit::Bool(value) => Ok(ModuleValue::Boolean(value.value)),
            _ => Err(default_kind(source)),
        },
        (Expr::Lit(expression), RustModuleType::Integer) => match &expression.lit {
            syn::Lit::Int(value) => value
                .base10_parse::<i64>()
                .map(ModuleValue::Integer)
                .map_err(|_| default_range(source)),
            _ => Err(default_kind(source)),
        },
        (Expr::Unary(unary), RustModuleType::Integer) if matches!(unary.op, syn::UnOp::Neg(_)) => {
            let Expr::Lit(expression) = unary.expr.as_ref() else {
                return Err(default_kind(source));
            };
            let syn::Lit::Int(value) = &expression.lit else {
                return Err(default_kind(source));
            };
            let magnitude = value
                .base10_parse::<u64>()
                .map_err(|_| default_range(source))?;
            let value = if magnitude == (i64::MAX as u64) + 1 {
                i64::MIN
            } else {
                -i64::try_from(magnitude).map_err(|_| default_range(source))?
            };
            Ok(ModuleValue::Integer(value))
        }
        (Expr::Lit(expression), RustModuleType::Float) => {
            let value = match &expression.lit {
                syn::Lit::Float(value) => value.base10_parse::<f64>(),
                syn::Lit::Int(value) => value.base10_parse::<f64>(),
                _ => return Err(default_kind(source)),
            }
            .map_err(|_| default_range(source))?;
            Number::from_f64(value)
                .map(ModuleValue::Float)
                .ok_or_else(|| default_range(source))
        }
        (Expr::Unary(unary), RustModuleType::Float) if matches!(unary.op, syn::UnOp::Neg(_)) => {
            let positive = default_value(unary.expr.as_ref(), ty, catalog, source)?;
            let ModuleValue::Float(value) = positive else {
                return Err(default_kind(source));
            };
            Number::from_f64(-value.as_f64().ok_or_else(|| default_range(source))?)
                .map(ModuleValue::Float)
                .ok_or_else(|| default_range(source))
        }
        (Expr::Array(array), RustModuleType::List(element)) => array
            .elems
            .iter()
            .map(|value| default_value(value, element, catalog, source))
            .collect::<Result<Vec<_>, _>>()
            .map(ModuleValue::List),
        (Expr::Path(path), RustModuleType::Optional(_)) if path.path.is_ident("None") => {
            Ok(ModuleValue::Optional(None))
        }
        (Expr::Call(call), RustModuleType::Optional(inner))
            if call_path_name(call).as_deref() == Some("Some") && call.args.len() == 1 =>
        {
            default_value(&call.args[0], inner, catalog, source)
                .map(|value| ModuleValue::Optional(Some(Box::new(value))))
        }
        (Expr::Path(path), RustModuleType::LocalEnum(symbol)) => {
            let variant = path
                .path
                .segments
                .last()
                .map(|segment| segment.ident.to_string())
                .ok_or_else(|| default_kind(source))?;
            let contract = catalog
                .enums
                .get(symbol)
                .ok_or_else(|| default_kind(source))?;
            let member = contract
                .wire_members()?
                .get(&variant)
                .cloned()
                .ok_or_else(|| default_kind(source))?;
            Ok(ModuleValue::LocalEnum {
                ty: symbol.clone(),
                member,
            })
        }
        (Expr::Call(call), RustModuleType::CustomScalar(symbol))
            if call.args.len() == 1
                && call_path_name(call).as_deref() == symbol.as_str().split("::").last() =>
        {
            let scalar = catalog
                .scalars
                .get(symbol)
                .ok_or_else(|| default_kind(source))?;
            scalar.validate()?;
            default_value(&call.args[0], &scalar.representation, catalog, source).map(|value| {
                ModuleValue::CustomScalar {
                    ty: symbol.clone(),
                    value: Box::new(value),
                }
            })
        }
        (Expr::Paren(paren), _) => default_value(&paren.expr, ty, catalog, source),
        (Expr::Group(group), _) => default_value(&group.expr, ty, catalog, source),
        _ => Err(default_error(
            Some(source.clone()),
            "the typed default uses unsupported syntax or the wrong value kind",
        )),
    }
}

fn call_path_name(call: &syn::ExprCall) -> Option<String> {
    let Expr::Path(path) = call.func.as_ref() else {
        return None;
    };
    path.path
        .segments
        .last()
        .map(|segment| segment.ident.to_string())
}

fn final_type_segment(spelling: &str) -> Option<&str> {
    spelling
        .split("::")
        .last()
        .map(str::trim)
        .filter(|segment| !segment.is_empty())
}

fn wire_name(
    explicit: Option<&String>,
    rust_name: &str,
    source: &SourceCoordinate,
) -> Result<WireName, ModuleDiagnostic> {
    let value = explicit
        .map(|value| string_literal(value, source))
        .transpose()?
        .unwrap_or_else(|| rust_name.to_case(Case::Camel));
    WireName::new(value).map_err(|_| {
        diagnostic(
            ModuleDiagnosticCode::NameInvalid,
            Some(source.clone()),
            "a normalized or explicit wire name is invalid",
            "use a non-empty target identifier",
        )
    })
}

fn merged_string_metadata(
    native: Option<&String>,
    authored: Option<&String>,
    source: &SourceCoordinate,
    _class: &'static str,
) -> Result<Option<String>, ModuleDiagnostic> {
    let authored = authored
        .map(|value| string_literal(value, source))
        .transpose()?;
    match (native, authored) {
        (Some(native), Some(authored)) if native.trim() != authored.trim() => Err(metadata_error(
            Some(source.clone()),
            "ordinary Rust and Dagger metadata declare conflicting semantic text",
        )),
        (Some(native), _) => Ok(Some(native.trim().to_owned())),
        (None, Some(authored)) => Ok(Some(authored.trim().to_owned())),
        (None, None) => Ok(None),
    }
}

fn optional_string(
    metadata: &BTreeMap<String, String>,
    name: &str,
    source: &SourceCoordinate,
) -> Result<Option<String>, ModuleDiagnostic> {
    metadata
        .get(name)
        .map(|value| string_literal(value, source))
        .transpose()
}

fn optional_strings(
    metadata: &BTreeMap<String, String>,
    name: &str,
    source: &SourceCoordinate,
) -> Result<Vec<String>, ModuleDiagnostic> {
    let Some(value) = metadata.get(name) else {
        return Ok(Vec::new());
    };
    let expression = syn::parse_str::<Expr>(value).map_err(|_| {
        metadata_error(
            Some(source.clone()),
            "ordered string metadata could not be parsed",
        )
    })?;
    let Expr::Array(array) = expression else {
        return Err(metadata_error(
            Some(source.clone()),
            "ignore metadata must be an ordered array of strings",
        ));
    };
    array
        .elems
        .iter()
        .map(|expression| match expression {
            Expr::Lit(literal) => match &literal.lit {
                syn::Lit::Str(value) => Ok(value.value()),
                _ => Err(metadata_error(
                    Some(source.clone()),
                    "ignore metadata contains a non-string pattern",
                )),
            },
            _ => Err(metadata_error(
                Some(source.clone()),
                "ignore metadata contains a non-literal pattern",
            )),
        })
        .collect()
}

fn parse_cache(
    value: Option<&String>,
    source: &SourceCoordinate,
) -> Result<CachePolicy, ModuleDiagnostic> {
    let Some(value) = value else {
        return Ok(CachePolicy::Default);
    };
    let value = string_literal(value, source)?;
    match value.as_str() {
        "" | "default" => Ok(CachePolicy::Default),
        "never" => Ok(CachePolicy::Never),
        "session" | "per-session" => Ok(CachePolicy::PerSession),
        _ => {
            let seconds = parse_duration_seconds(&value).ok_or_else(|| {
                metadata_error(
                    Some(source.clone()),
                    "cache TTL must use target duration syntax",
                )
            })?;
            if seconds == 0.0 {
                Ok(CachePolicy::Never)
            } else if seconds < 1.0 || seconds > MAX_CACHE_TTL_SECONDS as f64 {
                Err(metadata_error(
                    Some(source.clone()),
                    "cache TTL exceeds the target range of one second to one week",
                ))
            } else {
                Ok(CachePolicy::TimeToLive(seconds as u64))
            }
        }
    }
}

fn parse_duration_seconds(value: &str) -> Option<f64> {
    let mut remaining = value;
    let mut total = 0.0_f64;
    let mut components = 0_usize;
    while !remaining.is_empty() {
        let number_end = remaining
            .char_indices()
            .take_while(|(_, character)| character.is_ascii_digit() || *character == '.')
            .map(|(index, character)| index + character.len_utf8())
            .last()?;
        let number = remaining[..number_end].parse::<f64>().ok()?;
        if !number.is_finite() || number < 0.0 {
            return None;
        }
        remaining = &remaining[number_end..];
        let (unit, factor) = [
            ("ms", 0.001),
            ("us", 0.000_001),
            ("µs", 0.000_001),
            ("ns", 0.000_000_001),
            ("h", 3_600.0),
            ("m", 60.0),
            ("s", 1.0),
        ]
        .into_iter()
        .find(|(unit, _)| remaining.starts_with(unit))?;
        total += number * factor;
        if !total.is_finite() {
            return None;
        }
        remaining = &remaining[unit.len()..];
        components += 1;
    }
    (components > 0).then_some(total)
}

fn parse_role(
    value: Option<&String>,
    source: &SourceCoordinate,
) -> Result<FunctionRole, ModuleDiagnostic> {
    let Some(value) = value else {
        return Ok(FunctionRole::Ordinary);
    };
    match string_literal(value, source)?.as_str() {
        "ordinary" => Ok(FunctionRole::Ordinary),
        "check" => Ok(FunctionRole::Check),
        "generator" => Ok(FunctionRole::Generator),
        "up" => Ok(FunctionRole::Up),
        _ => Err(metadata_error(
            Some(source.clone()),
            "function role is not supported by the selected target",
        )),
    }
}

fn string_literal(value: &str, source: &SourceCoordinate) -> Result<String, ModuleDiagnostic> {
    syn::parse_str::<syn::LitStr>(value)
        .map(|value| value.value())
        .map_err(|_| {
            metadata_error(
                Some(source.clone()),
                "target string metadata must use a Rust string literal",
            )
        })
}

fn default_kind(source: &SourceCoordinate) -> ModuleDiagnostic {
    default_error(
        Some(source.clone()),
        "the typed default does not match its declared argument type",
    )
}

fn default_range(source: &SourceCoordinate) -> ModuleDiagnostic {
    diagnostic(
        ModuleDiagnosticCode::NumericOutOfRange,
        Some(source.clone()),
        "the typed numeric default is outside the target range",
        "use a finite f64 or signed 64-bit integer",
    )
}

fn default_error(source: Option<SourceCoordinate>, message: &'static str) -> ModuleDiagnostic {
    diagnostic(
        ModuleDiagnosticCode::DefaultInvalid,
        source,
        message,
        "use a supported typed literal accepted by the declared runtime codec",
    )
}

fn signature_error(source: Option<SourceCoordinate>, message: &'static str) -> ModuleDiagnostic {
    diagnostic(
        ModuleDiagnosticCode::FunctionSignatureInvalid,
        source,
        message,
        "use one concrete supported Rust function signature",
    )
}

fn metadata_error(source: Option<SourceCoordinate>, message: &'static str) -> ModuleDiagnostic {
    diagnostic(
        ModuleDiagnosticCode::FunctionMetadataInvalid,
        source,
        message,
        "remove the conflicting metadata or use one target-supported value",
    )
}

fn diagnostic(
    code: ModuleDiagnosticCode,
    source: Option<SourceCoordinate>,
    message: &'static str,
    remediation: &'static str,
) -> ModuleDiagnostic {
    ModuleDiagnostic::new(code, source, message, remediation)
        .expect("reviewed function diagnostics satisfy the safe renderer policy")
}

#[cfg(test)]
mod tests {
    use std::collections::{BTreeMap, BTreeSet};

    use proptest::prelude::*;

    use super::*;
    use crate::module::authoring::{AuthoringDeclaration, AuthoringParser};
    use crate::module::model::{ModuleSourcePath, Sha256Digest};
    use crate::module::source::ModuleDiscovery;
    use crate::module::types::{EnumContract, EnumVariantContract, ScalarContract};

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(256))]

        // Equivalent success types project identically while dispatch retains the exact direct/await choice.
        #[test]
        fn property_09_function_shape_independent_execution_syntax(
            asynchronous in any::<bool>(),
            fallible in any::<bool>(),
            unit in any::<bool>(),
            context in any::<bool>(),
            unsupported_receiver in any::<bool>(),
        ) {
            let async_token = if asynchronous { "async " } else { "" };
            let receiver = if unsupported_receiver { "&mut self" } else { "&self" };
            let context_parameter = if context {
                "#[dagger(context)] context: ModuleContext,"
            } else {
                ""
            };
            let success = if unit { "()" } else { "i64" };
            let output = if fallible {
                format!("Result<{success}, Error>")
            } else {
                success.to_owned()
            };
            let source = format!(r#"
                #[dagger_sdk::object(root)]
                pub struct Root {{}}

                #[dagger_sdk::methods]
                impl Root {{
                    #[dagger(function)]
                    pub {async_token}fn execute({receiver}, {context_parameter} value: i64) -> {output} {{
                        todo!()
                    }}
                }}
            "#);
            let parsed = parse(&source);
            if unsupported_receiver {
                prop_assert!(parsed.is_err());
                return Ok(());
            }
            let declarations = parsed.unwrap();
            let (discovery, function) = discovery_and_function(declarations);
            let catalog = TypeCatalog::default();
            let compiler = FunctionCompiler::new(TypeResolver::new(&discovery), &catalog);
            let compiled = compiler.compile(&discovery.root, &discovery.root, &function).unwrap();
            prop_assert_eq!(compiled.execution, if asynchronous { ExecutionKind::Asynchronous } else { ExecutionKind::Synchronous });
            prop_assert_eq!(compiled.inject_context, context);
            prop_assert_eq!(compiled.arguments.len(), 1);
            prop_assert_eq!(compiled.arguments[0].rust_name.as_str(), "value");
            prop_assert_eq!(compiled.return_type.success(), if unit { &RustModuleType::Void } else { &RustModuleType::Integer });
            prop_assert_eq!(matches!(compiled.return_type, FunctionReturn::Fallible { .. }), fallible);
        }

        // Metadata is either retained exactly or rejected at the authored argument that violates target policy.
        #[test]
        fn property_10_function_argument_metadata_exact_target_valid(
            asynchronous in any::<bool>(),
            renamed in any::<bool>(),
            required_deprecated in any::<bool>(),
            collision in any::<bool>(),
            role in 0_u8..4,
        ) {
            let async_token = if asynchronous { "async " } else { "" };
            let rename = if renamed { ", rename = \"enabledFlag\"" } else { "" };
            let first_metadata = if required_deprecated {
                format!("deprecated = \"use replacement\", doc = \"flag\"{rename}")
            } else {
                format!("default = false, deprecated = \"use replacement\", doc = \"flag\"{rename}")
            };
            let role_index = usize::from(role);
            let role = ["ordinary", "check", "generator", "up"][role_index];
            let second = if collision {
                ", foo_bar: bool, fooBar: bool"
            } else {
                ""
            };
            let source = format!(r#"
                #[dagger_sdk::object(root)]
                pub struct Root {{}}

                #[dagger_sdk::methods]
                impl Root {{
                    /// semantic docs
                    #[dagger(function, cache = "never", role = "{role}")]
                    pub {async_token}fn execute(
                        &self,
                        #[dagger({first_metadata})] enabled: bool{second}
                    ) -> bool {{
                        enabled
                    }}
                }}
            "#);
            let declarations = parse(&source).unwrap();
            let (discovery, function) = discovery_and_function(declarations);
            let catalog = TypeCatalog::default();
            let compiler = FunctionCompiler::new(TypeResolver::new(&discovery), &catalog);
            let compiled = compiler.compile(&discovery.root, &discovery.root, &function);
            if required_deprecated || collision {
                let error = compiled.unwrap_err();
                prop_assert!(matches!(error.code(), ModuleDiagnosticCode::FunctionMetadataInvalid | ModuleDiagnosticCode::WireNameCollision));
            } else {
                let compiled = compiled.unwrap();
                prop_assert_eq!(compiled.metadata.documentation.as_deref(), Some("semantic docs"));
                prop_assert_eq!(compiled.metadata.cache, CachePolicy::Never);
                prop_assert_eq!(compiled.metadata.role, [FunctionRole::Ordinary, FunctionRole::Check, FunctionRole::Generator, FunctionRole::Up][role_index]);
                prop_assert_eq!(compiled.arguments[0].wire_name.as_str(), if renamed { "enabledFlag" } else { "enabled" });
                prop_assert_eq!(&compiled.arguments[0].metadata.default, &Some(Value::Bool(false)));
                prop_assert_eq!(compiled.arguments[0].metadata.deprecation.as_deref(), Some("use replacement"));
                prop_assert_eq!(compiled.arguments[0].metadata.documentation.as_deref(), Some("flag"));
            }
        }
    }

    #[test]
    fn typed_defaults_use_enum_scalar_optional_and_list_codecs_without_evaluation() {
        let declarations = parse(
            r#"
            #[dagger_sdk::object(root)]
            pub struct Root {}

            #[dagger_sdk::enum_type]
            pub enum Mood { Happy, Quiet }

            #[dagger_sdk::scalar]
            pub struct Email(String);

            #[dagger_sdk::methods]
            impl Root {
                #[dagger(function)]
                pub fn defaults(
                    &self,
                    #[dagger(default = Mood::Happy)] mood: Mood,
                    #[dagger(default = Email("hello@example.com"))] email: Email,
                    #[dagger(default = Some(false))] enabled: Option<bool>,
                    #[dagger(default = [1, 2, 3])] values: Vec<i64>,
                ) -> bool { true }
            }
        "#,
        )
        .unwrap();
        let (discovery, function) = discovery_and_function(declarations);
        let mood = symbol("crate::Mood");
        let email = symbol("crate::Email");
        let catalog = TypeCatalog {
            enums: BTreeMap::from([(
                mood.clone(),
                EnumContract {
                    symbol: mood,
                    variants: vec![
                        EnumVariantContract {
                            rust_name: "Happy".to_owned(),
                            explicit_wire_name: None,
                        },
                        EnumVariantContract {
                            rust_name: "Quiet".to_owned(),
                            explicit_wire_name: None,
                        },
                    ],
                },
            )]),
            scalars: BTreeMap::from([(
                email.clone(),
                ScalarContract {
                    symbol: email,
                    representation: RustModuleType::String,
                },
            )]),
            ..TypeCatalog::default()
        };
        let compiler = FunctionCompiler::new(TypeResolver::new(&discovery), &catalog);
        let compiled = compiler
            .compile(&discovery.root, &discovery.root, &function)
            .unwrap();
        let defaults = compiled
            .arguments
            .iter()
            .map(|argument| argument.metadata.default.clone().unwrap())
            .collect::<Vec<_>>();
        assert_eq!(
            defaults,
            vec![
                Value::String("Happy".to_owned()),
                Value::String("hello@example.com".to_owned()),
                Value::Bool(false),
                Value::Array(vec![Value::from(1), Value::from(2), Value::from(3)]),
            ]
        );
    }

    #[test]
    fn arbitrary_default_calls_and_out_of_range_ttls_are_rejected() {
        let declarations = parse(
            r#"
            #[dagger_sdk::object(root)]
            pub struct Root {}

            #[dagger_sdk::methods]
            impl Root {
                #[dagger(function, cache = "169h")]
                pub fn invalid(&self, #[dagger(default = calculate())] value: i64) -> i64 { value }
            }
        "#,
        )
        .unwrap();
        let (discovery, function) = discovery_and_function(declarations);
        let catalog = TypeCatalog::default();
        let compiler = FunctionCompiler::new(TypeResolver::new(&discovery), &catalog);
        assert!(
            compiler
                .compile(&discovery.root, &discovery.root, &function)
                .is_err()
        );
    }

    #[test]
    fn merged_function_collisions_report_every_authored_coordinate() {
        let declarations = parse(
            r#"
            #[dagger_sdk::object(root)]
            pub struct Root {}

            #[dagger_sdk::methods]
            impl Root {
                #[dagger(function)]
                pub fn foo_bar(&self) -> bool { true }

                #[dagger(function)]
                pub fn fooBar(&self) -> bool { true }
            }
        "#,
        )
        .unwrap();
        let functions = declarations
            .iter()
            .find(|declaration| declaration.rust_symbol == symbol("crate::Root"))
            .unwrap()
            .functions
            .clone();
        let (discovery, _) = discovery_and_function(declarations);
        let catalog = TypeCatalog::default();
        let compiler = FunctionCompiler::new(TypeResolver::new(&discovery), &catalog);
        let diagnostics = compiler
            .compile_all(&discovery.root, &discovery.root, &functions)
            .unwrap_err();
        assert_eq!(diagnostics.diagnostics().len(), 2);
        assert!(diagnostics.diagnostics().iter().all(|diagnostic| {
            diagnostic.code() == ModuleDiagnosticCode::WireNameCollision
                && diagnostic.source_coordinate().is_some()
        }));
    }

    fn parse(
        source: &str,
    ) -> Result<Vec<AuthoringDeclaration>, super::super::diagnostic::ModuleDiagnosticSet> {
        AuthoringParser::parse(&ModuleSourcePath::new("src/lib.rs").unwrap(), source)
    }

    fn discovery_and_function(
        declarations: Vec<AuthoringDeclaration>,
    ) -> (ModuleDiscovery, AuthoringFunction) {
        let root = symbol("crate::Root");
        let function = declarations
            .iter()
            .find(|declaration| declaration.rust_symbol == root)
            .and_then(|declaration| declaration.functions.first())
            .cloned()
            .expect("fixture root carries one exported function");
        let declarations = declarations
            .into_iter()
            .map(|declaration| (declaration.rust_symbol.clone(), declaration))
            .collect();
        (
            ModuleDiscovery {
                root,
                declarations,
                interface_implementations: BTreeMap::new(),
                type_aliases: BTreeMap::new(),
                generated_types: BTreeMap::new(),
                visited_documents: BTreeSet::from([ModuleSourcePath::new("src/lib.rs").unwrap()]),
                source_digest: Sha256Digest::hash_bytes(b"metadata fixture"),
            },
            function,
        )
    }

    fn symbol(value: &str) -> RustSymbol {
        RustSymbol::new(value).expect("fixture symbol is canonical")
    }
}
