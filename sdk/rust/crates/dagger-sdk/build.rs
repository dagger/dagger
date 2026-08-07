use std::env;
use std::error::Error;
use std::fs;
use std::path::PathBuf;

fn main() -> Result<(), Box<dyn Error>> {
    let manifest_dir = PathBuf::from(env::var("CARGO_MANIFEST_DIR")?);
    let target_path = manifest_dir.join("../../completeness/target.json");
    let generated_path = manifest_dir.join("src/target_generated.rs");

    println!("cargo:rerun-if-changed={}", target_path.display());
    println!("cargo:rerun-if-changed={}", generated_path.display());
    println!("cargo:rerun-if-env-changed=DAGGER_RUST_UPDATE_TARGET");

    let target: serde_json::Value = serde_json::from_slice(&fs::read(&target_path)?)?;
    let engine_version = required_string(&target, "engine_version")?;
    let cli_version = required_string(&target, "rust_sdk_version")?;
    let revision = required_string(&target, "dagger_revision")?;

    if engine_version.strip_prefix('v') != Some(cli_version) {
        return Err("the Rust SDK and engine target versions must identify one release".into());
    }
    if revision.len() != 40
        || !revision
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
    {
        return Err(
            "the Dagger target revision must be 40 lowercase hexadecimal characters".into(),
        );
    }

    let expected = render(engine_version, cli_version, revision);
    if env::var_os("DAGGER_RUST_UPDATE_TARGET").as_deref() == Some(std::ffi::OsStr::new("1")) {
        fs::write(&generated_path, &expected)?;
    }
    let checked = fs::read_to_string(&generated_path)?;
    if checked != expected {
        return Err(format!(
            "{} has drifted from {}; run `DAGGER_RUST_UPDATE_TARGET=1 cargo check -p dagger-sdk` from sdk/rust and commit both files",
            generated_path.display(),
            target_path.display()
        )
        .into());
    }

    Ok(())
}

fn required_string<'a>(
    target: &'a serde_json::Value,
    field: &'static str,
) -> Result<&'a str, Box<dyn Error>> {
    target
        .get(field)
        .and_then(serde_json::Value::as_str)
        .ok_or_else(|| format!("target metadata field {field} is absent or is not text").into())
}

fn render(engine_version: &str, cli_version: &str, revision: &str) -> String {
    format!(
        "// @generated from sdk/rust/completeness/target.json; do not edit by hand.\n\
         \n\
         pub(super) const TARGET_ENGINE_VERSION: &str = {engine_version:?};\n\
         pub(super) const TARGET_CLI_VERSION: &str = {cli_version:?};\n\
         pub(super) const TARGET_REVISION: &str = {revision:?};\n"
    )
}
