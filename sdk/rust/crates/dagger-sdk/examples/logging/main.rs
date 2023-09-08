use std::sync::Arc;

use dagger_sdk::logging::TracingLogger;
use dagger_sdk::HostDirectoryOpts;

#[tokio::main]
async fn main() -> eyre::Result<()> {
    dagger_sdk::logging::default_logging()?;

    let client = dagger_sdk::connect_opts(dagger_sdk::Config {
        workdir_path: None,
        config_path: None,
        timeout_ms: 1000,
        execute_timeout_ms: None,
        logger: Some(Arc::new(TracingLogger::default())),
    })
    .await?;

    let host_source_dir = client.host().directory_opts(
        "examples/build-the-application/app",
        HostDirectoryOpts {
            exclude: Some(vec!["node_modules".into(), "ci/".into()]),
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

    let _ = build_dir.export("./build");

    let entries = build_dir.entries().await;

    println!("build dir contents: \n {:?}", entries);

    Ok(())
}
