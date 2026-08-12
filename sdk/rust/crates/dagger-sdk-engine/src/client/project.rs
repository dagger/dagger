//! Deterministic standalone-client Cargo identity selection.

use crate::{
    CargoPackageName, ClientProjectIdentity, EngineDiagnostic, EngineDiagnosticCode,
    RelativeOperationPath, RustIdentifier, StableCoordinate,
};

/// Borrowed semantic inputs used to select a standalone client's Cargo identity.
#[derive(Clone, Copy, Debug)]
pub struct ClientProjectIdentityRequest<'a> {
    /// Existing Cargo package name, when project discovery found one.
    pub existing_package_name: Option<&'a str>,
    /// Confined client root whose basename seeds a new package name.
    pub client_root: &'a RelativeOperationPath,
    /// Engine-normalized module name used only when the root has no usable basename.
    pub bound_module_name: &'a StableCoordinate,
}

/// Selects a Cargo package and crate identity without reading a filesystem.
pub fn select_client_project_identity(
    request: ClientProjectIdentityRequest<'_>,
) -> Result<ClientProjectIdentity, EngineDiagnostic> {
    let package_name = match request.existing_package_name {
        Some(existing) => CargoPackageName::new(existing.to_owned()).map_err(|_| {
            project_conflict("existing Cargo package name is incompatible with Rust client use")
        })?,
        None => {
            let basename = request
                .client_root
                .as_str()
                .rsplit('/')
                .next()
                .map(normalize_package_component)
                .filter(|value| !value.is_empty())
                .unwrap_or_else(|| normalize_package_component(request.bound_module_name.as_str()));
            CargoPackageName::new(format!("{basename}-dagger-client")).map_err(|_| {
                project_conflict("derived Cargo package name is incompatible with Rust client use")
            })?
        }
    };
    let crate_name =
        RustIdentifier::new(package_name.as_str().replace('-', "_")).map_err(|_| {
            project_conflict("Cargo package name does not normalize to a Rust crate identifier")
        })?;
    Ok(ClientProjectIdentity {
        package_name,
        crate_name,
    })
}

fn normalize_package_component(value: &str) -> String {
    let mut normalized = String::new();
    let mut separated = false;
    for byte in value.bytes() {
        if byte.is_ascii_alphanumeric() {
            normalized.push(char::from(byte.to_ascii_lowercase()));
            separated = false;
        } else if !normalized.is_empty() && !separated {
            normalized.push('-');
            separated = true;
        }
    }
    while normalized.ends_with('-') {
        normalized.pop();
    }
    normalized
}

fn project_conflict(message: &str) -> EngineDiagnostic {
    EngineDiagnostic::new(
        EngineDiagnosticCode::ClientProjectConflict,
        Some("package.name"),
        message,
    )
}
