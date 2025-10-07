use rand::Rng;

#[tokio::main]
async fn main() -> eyre::Result<()> {
    let mut rng = rand::thread_rng();

    dagger_sdk::connect(|client| async move {
        let context_dir = client
            .host()
            .directory("./examples/existing-dockerfile/app");

        let ref_ = context_dir
            .docker_build()
            .publish(format!("ttl.sh/hello-dagger-sdk-{}:1h", rng.gen::<u64>()))
            .await?;

        println!("published image to: {}", ref_);

        Ok(())
    })
    .await?;

    Ok(())
}
