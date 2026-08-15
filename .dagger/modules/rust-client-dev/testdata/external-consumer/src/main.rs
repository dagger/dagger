use std::error::Error;

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    let client = dagger_sdk::connect().await?;
    let version = client.query().version().await?;
    if version.trim().is_empty() {
        return Err("complete engine returned an empty version".into());
    }
    client.close().await?;
    println!("{version}");
    Ok(())
}
