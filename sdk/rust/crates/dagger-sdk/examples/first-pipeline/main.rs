#[tokio::main]
async fn main() -> eyre::Result<()> {
    tracing_subscriber::fmt::init();

    dagger_sdk::connect(|client| async move {
        let version = client
            .container()
            .from("golang:1.19")
            .with_exec(vec!["go", "version"])
            .stdout()
            .await?;

        println!("Hello from Dagger and {}", version.trim());

        Ok(())
    })
    .await?;

    Ok(())
}
