#[tokio::main]
async fn main() -> eyre::Result<()> {
    dagger_sdk::connect(|client| async move {
        let app_directory = client.host().directory("./app");

        let build_file = client
            .container()
            .from("rust:1.97.1-slim-bookworm")
            .with_directory("./app", app_directory)
            .with_workdir("/app")
            .with_exec(vec!["cargo", "build", "--release"])
            .file("./target/release/app");

        build_file.export("./build/cli").await?;
        Ok(())
    })
    .await?;

    Ok(())
}
