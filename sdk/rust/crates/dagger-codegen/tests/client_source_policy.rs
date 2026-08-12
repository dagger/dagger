//! Fixed source policy for the pure standalone-client compiler and renderer.

const CLIENT_SOURCES: &[(&str, &str)] = &[
    (
        "client/compiler.rs",
        include_str!("../src/client/compiler.rs"),
    ),
    ("client/model.rs", include_str!("../src/client/model.rs")),
    ("client/mod.rs", include_str!("../src/client/mod.rs")),
    ("client/naming.rs", include_str!("../src/client/naming.rs")),
    ("client/render.rs", include_str!("../src/client/render.rs")),
    ("engine/client.rs", include_str!("../src/engine/client.rs")),
];

#[test]
fn production_client_sources_obey_the_safe_documented_boundary() {
    for (path, source) in CLIENT_SOURCES {
        assert!(
            source.starts_with("//!"),
            "{path} needs module documentation"
        );
        for forbidden in [
            "unsafe {",
            ".unwrap(",
            ".expect(",
            "panic!(",
            "todo!(",
            "unimplemented!(",
            "SessionHandle",
            "authorization",
            "Feature ",
            "Task ",
            "Property ",
        ] {
            assert!(
                !source.contains(forbidden),
                "{path} contains forbidden source fragment {forbidden}"
            );
        }
    }
}
