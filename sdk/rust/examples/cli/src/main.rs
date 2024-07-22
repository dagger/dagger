#[tokio::main]
async fn main() -> eyre::Result<()> {
    let client = dagger_sdk::connect().await?;

    let app_directory = client.host().directory("./app");

    let build_file = client
        .container()
        .from("rust:1.77.2-slim-buster")
        .with_directory("./app", app_directory)
        .with_workdir("/app")
        .with_exec(vec!["cargo", "build", "--release"])
        .file("./target/release/app");

    build_file.export("./build/cli").await?;

    Ok(())
}
