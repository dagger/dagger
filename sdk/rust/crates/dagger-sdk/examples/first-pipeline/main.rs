#[tokio::main]
async fn main() -> eyre::Result<()> {
    tracing_subscriber::fmt::init();

    let owned = dagger_sdk::connect().await?;
    let client = owned.query();
    let version = client
        .container()
        .from("golang:1.19")
        .with_exec(vec!["go", "version"])
        .stdout()
        .await?;

    println!("Hello from Dagger and {}", version.trim());

    owned.close().await?;

    Ok(())
}
