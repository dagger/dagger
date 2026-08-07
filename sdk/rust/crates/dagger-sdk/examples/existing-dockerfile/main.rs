use rand::Rng;

#[tokio::main]
async fn main() -> eyre::Result<()> {
    let mut rng = rand::thread_rng();

    let owned = dagger_sdk::connect().await?;
    let client = owned.query();
    let context_dir = client
        .host()
        .directory("./examples/existing-dockerfile/app");

    let ref_ = context_dir
        .docker_build()
        .publish(format!("ttl.sh/hello-dagger-sdk-{}:1h", rng.r#gen::<u64>()))
        .await?;

    println!("published image to: {}", ref_);

    owned.close().await?;

    Ok(())
}
