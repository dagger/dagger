use rand::Rng;

fn main() -> eyre::Result<()> {
    let client = dagger_sdk::connect()?;

    let host_source_dir = client.host().directory(
        "./examples/caching/app",
        Some(
            dagger_sdk::HostDirectoryOptsBuilder::default()
                .exclude(vec!["node_modules", "ci/"])
                .build()?,
        ),
    );

    let node_cache = client.cache_volume("node").id()?;

    let source = client
        .container(None)
        .from("node:16")
        .with_mounted_directory("/src", host_source_dir.id()?)
        .with_mounted_cache("/src/node_modules", node_cache, None);

    let runner = source
        .with_workdir("/src")
        .with_exec(vec!["npm", "install"], None);

    let test = runner.with_exec(vec!["npm", "test", "--", "--watchAll=false"], None);

    let build_dir = test
        .with_exec(vec!["npm", "run", "build"], None)
        .directory("./build");

    let mut rng = rand::thread_rng();

    let ref_ = client
        .container(None)
        .from("nginx")
        .with_directory("/usr/share/nginx/html", build_dir.id()?, None)
        .publish(
            format!("ttl.sh/hello-dagger-rs-{}:1h", rng.gen::<u64>()),
            None,
        )?;

    println!("published image to: {}", ref_);

    Ok(())
}
