use dagger_sdk::HostDirectoryOpts;
use rand::Rng;

#[tokio::main]
async fn main() -> eyre::Result<()> {
    let client = dagger_sdk::connect().await?;

    let host_source_dir = client.host().directory_opts(
        "examples/publish-the-application/app",
        HostDirectoryOpts {
            exclude: Some(vec!["node_modules", "ci/"]),
            include: None,
        },
    );

    let source = client
        .container()
        .from("node:16")
        .with_mounted_directory("/src", host_source_dir);

    let runner = source
        .with_workdir("/src")
        .with_exec(vec!["npm", "install"]);

    let test = runner.with_exec(vec!["npm", "test", "--", "--watchAll=false"]);

    let build_dir = test
        .with_exec(vec!["npm", "run", "build"])
        .directory("./build");

    let mut rng = rand::thread_rng();

    let ref_ = client
        .container()
        .from("nginx")
        .with_directory("/usr/share/nginx/html", build_dir)
        .publish(format!("ttl.sh/hello-dagger-sdk-{}:1h", rng.gen::<u64>()))
        .await?;

    println!("published image to: {}", ref_);

    Ok(())
}
