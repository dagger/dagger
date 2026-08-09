use dagger_sdk::HostDirectoryOpts;

#[tokio::main]
async fn main() -> eyre::Result<()> {
    let owned = dagger_sdk::connect().await?;
    let client = owned.query();
    let host_source_dir = client.host().directory_opts(
        "examples/build-the-application/app",
        &HostDirectoryOpts::default().with_exclude(vec!["node_modules", "ci/"]),
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

    let entries = build_dir.entries().await;

    println!("build dir contents: \n {:?}", entries);

    owned.close().await?;

    Ok(())
}
