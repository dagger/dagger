use dagger_sdk::HostDirectoryOpts;

fn main() -> eyre::Result<()> {
    let client = dagger_sdk::connect()?;

    let host_source_dir = client.host().directory_opts(
        "examples/test-the-application/app",
        HostDirectoryOpts {
            exclude: Some(vec!["node_modules", "ci/"]),
            include: None,
        },
    );

    let source = client
        .container()
        .from("node:16")
        .with_mounted_directory("/src", host_source_dir.id()?);

    let runner = source
        .with_workdir("/src")
        .with_exec(vec!["npm", "install"]);

    let out = runner
        .with_exec(vec!["npm", "test", "--", "--watchAll=false"])
        .stderr()?;

    println!("{}", out);

    Ok(())
}
