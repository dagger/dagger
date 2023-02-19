use dagger_sdk::HostDirectoryOpts;

fn main() -> eyre::Result<()> {
    let client = dagger_sdk::connect()?;

    let host_source_dir = client.host().directory(
        "examples/test-the-application/app",
        Some(HostDirectoryOpts {
            exclude: Some(vec!["node_modules", "ci/"]),
            include: None,
        }),
    );

    let source = client
        .container(None)
        .from("node:16")
        .with_mounted_directory("/src", host_source_dir.id()?);

    let runner = source
        .with_workdir("/src")
        .with_exec(vec!["npm", "install"], None);

    let out = runner
        .with_exec(vec!["npm", "test", "--", "--watchAll=false"], None)
        .stderr()?;

    println!("{}", out);

    Ok(())
}
