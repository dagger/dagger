use std::time::Duration;

use dagger_sdk::{ClientConfig, HostDirectoryOpts};

#[tokio::main]
async fn main() -> eyre::Result<()> {
    tracing_subscriber::fmt::try_init()
        .map_err(|_| eyre::eyre!("failed to install the tracing subscriber"))?;
    let config = ClientConfig::builder()
        .session_startup_timeout(Duration::from_secs(30))
        .build()?;
    let owned = dagger_sdk::connect_with(config).await?;
    let client = owned.query();

    let host_source_dir = client.host().directory_opts(
        "examples/build-the-application/app",
        HostDirectoryOpts {
            exclude: Some(vec!["node_modules", "ci/"]),
            include: None,
            no_cache: None,
            gitignore: None,
        },
    );
    let build_dir = client
        .container()
        .from("node:16")
        .with_mounted_directory("/src", host_source_dir)
        .with_workdir("/src")
        .with_exec(vec!["npm", "install"])
        .with_exec(vec!["npm", "test", "--", "--watchAll=false"])
        .with_exec(vec!["npm", "run", "build"])
        .directory("./build");
    println!("build dir contents: \n {:?}", build_dir.entries().await);

    owned.close().await?;
    Ok(())
}
