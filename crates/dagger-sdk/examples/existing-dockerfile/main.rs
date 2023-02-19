use rand::Rng;

fn main() -> eyre::Result<()> {
    let mut rng = rand::thread_rng();

    let client = dagger_sdk::connect()?;

    let context_dir = client
        .host()
        .directory("./examples/existing-dockerfile/app");

    let ref_ = client
        .container()
        .build(context_dir.id()?)
        .publish(format!("ttl.sh/hello-dagger-rs-{}:1h", rng.gen::<u64>()))?;

    println!("published image to: {}", ref_);

    Ok(())
}
