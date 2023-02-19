use dagger_sdk::gen::HostDirectoryOpts;
use rand::Rng;

fn main() -> eyre::Result<()> {
    let client = dagger_sdk::client::connect()?;

    let host_source_dir = client.host().directory(
        "./examples/caching/app".into(),
        Some(HostDirectoryOpts {
            exclude: Some(vec!["node_modules".into(), "ci/".into()]),
            include: None,
        }),
    );

    let node_cache = client.cache_volume("node".into()).id()?;

    let source = client
        .container(None)
        .from("node:16".into())
        .with_mounted_directory("/src".into(), host_source_dir.id()?)
        .with_mounted_cache("/src/node_modules".into(), node_cache, None);

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

    let mut rng = rand::thread_rng();

    let ref_ = client
        .container(None)
        .from("nginx".into())
        .with_directory("/usr/share/nginx/html".into(), build_dir.id()?, None)
        .publish(
            format!("ttl.sh/hello-dagger-rs-{}:1h", rng.gen::<u64>()),
            None,
        )?;

    println!("published image to: {}", ref_);

    Ok(())
}
