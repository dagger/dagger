//! Data-only standalone-client surface model regressions.

use std::collections::BTreeSet;

use dagger_codegen::client::{
    CargoPackageName, ClientNamespaceRecord, ClientProjectIdentity, ClientSchemaSurface,
    ModuleRoot, ModuleSurfacePlan, RustIdentifier,
};
use dagger_codegen::schema::canonical::{SchemaCoordinate, SchemaName};

#[test]
fn client_surface_models_round_trip_strictly() {
    let query = SchemaName::try_from("Query").unwrap();
    let module = SchemaName::try_from("minimal").unwrap();
    let object = SchemaName::try_from("Minimal").unwrap();
    let root = ModuleRoot {
        field_coordinate: SchemaCoordinate::field(&query, &module),
        field_wire_name: module,
        object_wire_name: object.clone(),
        object_coordinate: SchemaCoordinate::named_type(&object),
    };
    let surface = ClientSchemaSurface::BoundModule(ModuleSurfacePlan {
        closure: BTreeSet::from([
            root.field_coordinate.clone(),
            root.object_coordinate.clone(),
        ]),
        root,
        namespace: ClientNamespaceRecord {
            namespace: RustIdentifier::new("minimal").unwrap(),
            extension_trait: RustIdentifier::new("MinimalExt").unwrap(),
            root_type: RustIdentifier::new("Client").unwrap(),
        },
    });
    let bytes = serde_json::to_vec(&surface).unwrap();
    assert_eq!(
        serde_json::from_slice::<ClientSchemaSurface>(&bytes).unwrap(),
        surface
    );

    let mut invalid: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
    invalid["module"]["ambient_path"] = serde_json::json!("/tmp/project");
    assert!(serde_json::from_value::<ClientSchemaSurface>(invalid).is_err());
}

#[test]
fn project_identity_rejects_unknown_wire_fields() {
    let valid = serde_json::json!({
        "package_name": "minimal-dagger-client",
        "crate_name": "minimal_dagger_client"
    });
    assert!(serde_json::from_value::<ClientProjectIdentity>(valid.clone()).is_ok());
    let mut invalid = valid;
    invalid["session_token"] = serde_json::json!("secret");
    assert!(serde_json::from_value::<ClientProjectIdentity>(invalid).is_err());

    for invalid in ["", "async", "9client", "client!"] {
        assert!(CargoPackageName::new(invalid).is_err());
        assert!(RustIdentifier::new(invalid).is_err());
    }
}
