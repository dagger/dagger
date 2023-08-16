use rand::Rng;

#[tokio::main]
async fn main() -> eyre::Result<()> {
    let mut rng = rand::thread_rng();

    let client = dagger_sdk::connect().await?;

    let context_dir = client
        .host()
        .directory("./examples/existing-dockerfile/app");

    let ref_ = client
        .container()
        .build(context_dir)
        .publish(format!("ttl.sh/hello-dagger-sdk-{}:1h", rng.gen::<u64>()))
        .await?;

    println!("published image to: {}", ref_);

    Ok(())
}
