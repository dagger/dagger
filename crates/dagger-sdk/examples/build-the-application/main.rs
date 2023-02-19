use dagger_sdk::gen::HostDirectoryOpts;

fn main() -> eyre::Result<()> {
    let client = dagger_sdk::client::connect()?;

    let host_source_dir = client.host().directory(
        "examples/build-the-application/app".into(),
        Some(HostDirectoryOpts {
            exclude: Some(vec!["node_modules".into(), "ci/".into()]),
            include: None,
        }),
    );

    let source = client
        .container(None)
        .from("node:16".into())
        .with_mounted_directory("/src".into(), host_source_dir.id()?);

    let runner = source
        .with_workdir("/src".into())
        .with_exec(vec!["npm".into(), "install".into()], None);

    let test = runner.with_exec(
        vec![
            "npm".into(),
            "test".into(),
            "--".into(),
            "--watchAll=false".into(),
        ],
        None,
    );

    let build_dir = test
        .with_exec(vec!["npm".into(), "run".into(), "build".into()], None)
        .directory("./build".into());

    let _ = build_dir.export("./build".into());

    let entries = build_dir.entries(None);

    println!("build dir contents: \n {:?}", entries);

    Ok(())
}
