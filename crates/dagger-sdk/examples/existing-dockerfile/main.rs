use rand::Rng;

fn main() -> eyre::Result<()> {
    let mut rng = rand::thread_rng();

    let client = dagger_sdk::connect()?;

    let context_dir = client
        .host()
        .directory("./examples/existing-dockerfile/app".into(), None);

    let ref_ = client
        .container(None)
        .build(context_dir.id()?, None)
        .publish(
            format!("ttl.sh/hello-dagger-rs-{}:1h", rng.gen::<u64>()),
            None,
        )?;

    println!("published image to: {}", ref_);

    Ok(())
}
