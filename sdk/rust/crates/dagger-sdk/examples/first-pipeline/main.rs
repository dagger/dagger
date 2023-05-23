#[tokio::main]
async fn main() -> eyre::Result<()> {
    let client = dagger_sdk::connect().await?;

    let version = client
        .container()
        .from("golang:1.19")
        .with_exec(vec!["go", "version"])
        .stdout()
        .await?;

    println!("Hello from Dagger and {}", version.trim());

    Ok(())
}
