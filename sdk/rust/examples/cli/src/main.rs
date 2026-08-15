const BUILDER_IMAGE: &str = "rust:1.97.1-slim-bookworm";
const OUTPUT: &str = "build/cli";

#[tokio::main]
async fn main() -> eyre::Result<()> {
    let owned = dagger_sdk::connect().await?;
    let client = owned.query();
    let app_directory = client.host().directory("./app");

    let build_file = client
        .container()
        .from(BUILDER_IMAGE)
        .with_directory("./app", app_directory)
        .with_workdir("/app")
        .with_exec(vec!["cargo", "build", "--release", "--locked"])
        .file("./target/release/app");

    let exported = build_file.export(format!("./{OUTPUT}")).await?;
    if exported.is_empty() {
        eyre::bail!("Dagger did not export the built CLI");
    }
    println!("CLI built at {exported}");
    owned.close().await?;

    Ok(())
}
