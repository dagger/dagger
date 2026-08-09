use rand::Rng;

#[tokio::main]
async fn main() -> eyre::Result<()> {
    let owned = dagger_sdk::connect().await?;
    let client = owned.query();
    let host_source_dir = client.host().directory_opts(
        "./examples/caching/app",
        &dagger_sdk::HostDirectoryOpts::default().with_exclude(vec!["node_modules/", "ci/"]),
    );

    let node_cache = client.cache_volume("node");

    let source = client
        .container()
        .from("node:16")
        .with_mounted_directory("/src", host_source_dir)
        .with_mounted_cache(node_cache, "/root/.npm");

    let runner = source
        .with_workdir("/src")
        .with_exec(vec!["npm", "install"]);

    let test = runner.with_exec(vec!["npm", "test", "--", "--watchAll=false"]);

    let build_dir = test
        .with_exec(vec!["npm", "run", "build"])
        .directory("./build");

    let mut rng = rand::thread_rng();

    let r#ref = client
        .container()
        .from("nginx")
        .with_directory("/usr/share/nginx/html", build_dir)
        .publish(format!("ttl.sh/hello-dagger-sdk-{}:1h", rng.r#gen::<u64>()))
        .await?;

    println!("published image to: {}", r#ref);

    owned.close().await?;

    Ok(())
}
