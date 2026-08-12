//! Pure selection, module binding, and aggregate admission for workspace clients.
//!
//! The Go ABI retains transient Dagger objects, but Rust owns every durable decision:
//! cwd containment, canonical order, overlap rejection, stored-pin equality, and the
//! all-or-nothing admission of independently produced client results. No type in this
//! module can carry a raw module reference, engine object, filesystem handle, or secret.

use std::collections::{BTreeMap, BTreeSet};

use crate::{
    ClientModuleIdentity, ClientSetPlan, EngineDiagnostic, EngineDiagnosticCode, FormatVersion,
    PlanClientSetRequest, PlannedClient, RelativeOperationPath, Sha256Digest,
};

/// One independently completed client operation offered to aggregate admission.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ClientOperationOutcome {
    /// Stable record identity from the preflight plan.
    pub record_index: u32,
    /// Exact client root processed by this operation.
    pub path: RelativeOperationPath,
    /// Digest of the complete independently published client manifest.
    pub manifest_digest: Sha256Digest,
    /// Whether the isolated operation completed successfully.
    pub passed: bool,
}

/// Complete aggregate made available only after every selected client passed.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ClientSetOutcome {
    /// Canonically path-ordered independent client results.
    pub clients: Vec<ClientOperationOutcome>,
}

/// Selects descendants of cwd, sorts them canonically, and rejects aliases or overlap.
pub fn plan_client_set(request: PlanClientSetRequest) -> Result<ClientSetPlan, EngineDiagnostic> {
    let mut indices = BTreeSet::new();
    for client in &request.clients {
        if !indices.insert(client.record_index) {
            return Err(client_error(
                EngineDiagnosticCode::OperationInputInvalid,
                "clients.record_index",
                "workspace client record index is duplicated",
            ));
        }
    }
    let mut selected = request
        .clients
        .into_iter()
        .filter(|client| path_is_at_or_below(&request.cwd, &client.path))
        .map(|client| PlannedClient {
            record_index: client.record_index,
            path: client.path,
            module_ref_digest: client.module_ref_digest,
            stored_pin: client.stored_pin,
        })
        .collect::<Vec<_>>();
    selected.sort_by(|left, right| left.path.cmp(&right.path));
    for pair in selected.windows(2) {
        if paths_overlap(&pair[0].path, &pair[1].path) {
            return Err(client_error(
                EngineDiagnosticCode::ClientRootOverlap,
                pair[1].path.as_str(),
                "managed standalone-client roots overlap",
            ));
        }
    }
    Ok(ClientSetPlan {
        format_version: FormatVersion,
        cwd: request.cwd,
        clients: selected,
    })
}

/// Binds one selected record to exactly one resolved module identity.
pub fn bind_client_module(
    record: &PlannedClient,
    modules: &[ClientModuleIdentity],
) -> Result<ClientModuleIdentity, EngineDiagnostic> {
    let [module] = modules else {
        return Err(client_error(
            EngineDiagnosticCode::OperationInputInvalid,
            record.path.as_str(),
            "a standalone client must resolve exactly one bound module",
        ));
    };
    if record.stored_pin != module.resolved_pin {
        return Err(client_error(
            EngineDiagnosticCode::ClientPinMismatch,
            record.path.as_str(),
            "stored client pin differs from the resolved module revision",
        ));
    }
    Ok(module.clone())
}

/// Admits the aggregate only when every planned client has one matching passed result.
pub fn admit_client_set(
    plan: &ClientSetPlan,
    outcomes: Vec<ClientOperationOutcome>,
) -> Result<ClientSetOutcome, EngineDiagnostic> {
    let mut observed = BTreeMap::new();
    for outcome in outcomes {
        if !outcome.passed || observed.insert(outcome.record_index, outcome).is_some() {
            return Err(client_error(
                EngineDiagnosticCode::ClientFixtureFailed,
                "clients",
                "client generation failed or produced a duplicate aggregate result",
            ));
        }
    }
    if observed.len() != plan.clients.len() {
        return Err(client_error(
            EngineDiagnosticCode::ClientFixtureFailed,
            "clients",
            "aggregate result does not account for every selected client",
        ));
    }
    let mut clients = Vec::with_capacity(plan.clients.len());
    for selected in &plan.clients {
        let Some(outcome) = observed.remove(&selected.record_index) else {
            return Err(client_error(
                EngineDiagnosticCode::ClientFixtureFailed,
                selected.path.as_str(),
                "selected client has no isolated operation result",
            ));
        };
        if outcome.path != selected.path {
            return Err(client_error(
                EngineDiagnosticCode::ClientFixtureFailed,
                selected.path.as_str(),
                "client operation result belongs to a different root",
            ));
        }
        clients.push(outcome);
    }
    Ok(ClientSetOutcome { clients })
}

fn path_is_at_or_below(cwd: &RelativeOperationPath, path: &RelativeOperationPath) -> bool {
    path == cwd
        || path
            .as_str()
            .strip_prefix(cwd.as_str())
            .is_some_and(|suffix| suffix.starts_with('/'))
}

fn paths_overlap(left: &RelativeOperationPath, right: &RelativeOperationPath) -> bool {
    path_is_at_or_below(left, right) || path_is_at_or_below(right, left)
}

fn client_error(
    code: EngineDiagnosticCode,
    coordinate: &str,
    message: &'static str,
) -> EngineDiagnostic {
    EngineDiagnostic::new(code, Some(coordinate), message)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sibling_prefixes_do_not_overlap() {
        let left = RelativeOperationPath::parse("workspace/client").unwrap();
        let right = RelativeOperationPath::parse("workspace/client-two").unwrap();
        assert!(!paths_overlap(&left, &right));
    }

    #[test]
    fn empty_workspace_selection_is_a_successful_noop() {
        let cwd = RelativeOperationPath::parse("workspace").unwrap();
        let plan = plan_client_set(PlanClientSetRequest {
            format_version: FormatVersion,
            cwd,
            clients: Vec::new(),
        })
        .unwrap();
        assert!(plan.clients.is_empty());
    }

    #[test]
    fn duplicate_identity_is_rejected_even_outside_the_selected_cwd() {
        let path = RelativeOperationPath::parse("workspace/other/client").unwrap();
        let input = crate::ManagedClientInput {
            record_index: 4,
            path,
            module_ref_digest: Sha256Digest::new(format!("sha256:{}", "00".repeat(32))).unwrap(),
            stored_pin: None,
        };
        let error = plan_client_set(PlanClientSetRequest {
            format_version: FormatVersion,
            cwd: RelativeOperationPath::parse("workspace/selected").unwrap(),
            clients: vec![input.clone(), input],
        })
        .unwrap_err();
        assert_eq!(error.code, EngineDiagnosticCode::OperationInputInvalid);
    }
}
