//! Workspace-client selection, binding, isolation, and aggregate properties.

use dagger_sdk_engine::{
    ClientModuleIdentity, ClientOperationOutcome, EngineDiagnosticCode, FormatVersion,
    ManagedClientInput, PlanClientSetRequest, PlannedClient, RelativeOperationPath, Sha256Digest,
    StableCoordinate, admit_client_set, bind_client_module, plan_client_set,
};
use proptest::prelude::*;

fn digest(seed: u16, domain: u8) -> Sha256Digest {
    format!("sha256:{:064x}", (u32::from(seed) << 8) | u32::from(domain))
        .parse()
        .unwrap()
}

fn revision(seed: u16) -> dagger_sdk_engine::FullRevision {
    format!("{seed:040x}").parse().unwrap()
}

fn module(seed: u16, remote: bool) -> ClientModuleIdentity {
    ClientModuleIdentity {
        name: StableCoordinate::new(format!("module-{seed}")).unwrap(),
        original_name: StableCoordinate::new(format!("Module {seed}")).unwrap(),
        source_subpath: RelativeOperationPath::parse(&format!("workspace/modules/{seed}")).unwrap(),
        source_digest: digest(seed, 1),
        resolved_pin: remote.then(|| revision(seed)),
    }
}

proptest! {
    #![proptest_config(ProptestConfig::with_cases(256))]

    // One client record admits exactly one module and requires exact remote-pin equality.
    #[test]
    fn property_04_workspace_record_resolves_one_exact_bound_module(
        seed in any::<u16>(),
        remote in any::<bool>(),
        mutation in 0_u8..5,
    ) {
        let expected = module(seed, remote);
        let mut record = PlannedClient {
            record_index: u32::from(seed),
            path: RelativeOperationPath::parse(&format!("workspace/clients/{seed}")).unwrap(),
            module_ref_digest: digest(seed, 2),
            stored_pin: expected.resolved_pin.clone(),
        };
        let mut modules = vec![expected.clone()];
        match mutation {
            0 => {}
            1 => modules.clear(),
            2 => modules.push(module(seed.wrapping_add(1), remote)),
            3 => record.stored_pin = Some(revision(seed.wrapping_add(1))),
            4 => {
                if remote {
                    record.stored_pin = None;
                } else {
                    record.stored_pin = Some(revision(seed));
                }
            }
            _ => unreachable!(),
        }
        let result = bind_client_module(&record, &modules);
        prop_assert_eq!(result.is_ok(), mutation == 0);
        if let Ok(bound) = result {
            prop_assert_eq!(bound, expected);
        }
    }

    // Cwd selection is path-canonical and the closed result can contain only client records.
    #[test]
    fn property_17_workspace_cwd_selection_canonical_rust_only(
        seed in any::<u16>(),
        reverse in any::<bool>(),
        outside_count in 0_u8..8,
    ) {
        let cwd = RelativeOperationPath::parse("workspace/apps").unwrap();
        let mut clients = (0_u32..4)
            .map(|index| ManagedClientInput {
                record_index: index,
                path: RelativeOperationPath::parse(&format!("workspace/apps/client-{index}-{seed}")).unwrap(),
                module_ref_digest: digest(seed, u8::try_from(index + 1).unwrap()),
                stored_pin: None,
            })
            .collect::<Vec<_>>();
        clients.extend((0..outside_count).map(|index| ManagedClientInput {
            record_index: 100 + u32::from(index),
            path: RelativeOperationPath::parse(&format!("workspace/other/client-{index}-{seed}")).unwrap(),
            module_ref_digest: digest(seed.wrapping_add(u16::from(index)), 9),
            stored_pin: None,
        }));
        if reverse {
            clients.reverse();
        }
        let plan = plan_client_set(PlanClientSetRequest {
            format_version: FormatVersion,
            cwd: cwd.clone(),
            clients,
        }).unwrap();
        prop_assert_eq!(plan.cwd, cwd);
        prop_assert_eq!(plan.clients.len(), 4);
        prop_assert!(plan.clients.windows(2).all(|pair| pair[0].path < pair[1].path));
        prop_assert!(plan.clients.iter().all(|client| client.path.as_str().starts_with("workspace/apps/")));
    }

    // Overlap fails before admission and one failed sibling makes the aggregate unavailable.
    #[test]
    fn property_18_multiple_clients_isolated_all_or_nothing(
        seed in any::<u16>(),
        overlap in any::<bool>(),
        failure in 0_usize..5,
    ) {
        let second = if overlap {
            format!("workspace/clients/{seed}/nested")
        } else {
            format!("workspace/clients/{}", seed.wrapping_add(1))
        };
        let request = PlanClientSetRequest {
            format_version: FormatVersion,
            cwd: RelativeOperationPath::parse("workspace/clients").unwrap(),
            clients: vec![
                ManagedClientInput {
                    record_index: 0,
                    path: RelativeOperationPath::parse(&format!("workspace/clients/{seed}")).unwrap(),
                    module_ref_digest: digest(seed, 1),
                    stored_pin: None,
                },
                ManagedClientInput {
                    record_index: 1,
                    path: RelativeOperationPath::parse(&second).unwrap(),
                    module_ref_digest: digest(seed, 2),
                    stored_pin: None,
                },
            ],
        };
        let planned = plan_client_set(request);
        prop_assert_eq!(planned.is_err(), overlap);
        if overlap {
            prop_assert_eq!(planned.unwrap_err().code, EngineDiagnosticCode::ClientRootOverlap);
            return Ok(());
        }
        let plan = planned.unwrap();
        let outcomes = plan.clients.iter().enumerate().map(|(index, client)| ClientOperationOutcome {
            record_index: client.record_index,
            path: client.path.clone(),
            manifest_digest: digest(seed, u8::try_from(index + 3).unwrap()),
            passed: failure != index,
        }).collect::<Vec<_>>();
        let aggregate = admit_client_set(&plan, outcomes);
        prop_assert_eq!(aggregate.is_ok(), failure >= plan.clients.len());
        if let Ok(aggregate) = aggregate {
            prop_assert_eq!(aggregate.clients.len(), 2);
            prop_assert_ne!(&aggregate.clients[0].manifest_digest, &aggregate.clients[1].manifest_digest);
        }
    }
}
