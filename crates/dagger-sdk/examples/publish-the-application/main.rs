use dagger_sdk::HostDirectoryOpts;
use rand::Rng;

fn main() -> eyre::Result<()> {
    let client = dagger_sdk::connect()?;
    let output = "examples/publish-the-application/app/build";

    let host_source_dir = client.host().directory(
        "examples/publish-the-application/app",
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

    let test = runner.with_exec(vec!["npm", "test", "--", "--watchAll=false"], None);

    let _ = test
        .with_exec(vec!["npm", "run", "build"], None)
        .directory("./build")
        .export(output);

    let mut rng = rand::thread_rng();

    let ref_ = client
        .container(None)
        .from("nginx")
        .with_directory(
            "/usr/share/nginx/html",
            client.host().directory(output, None).id()?,
            None,
        )
        .publish(
            format!("ttl.sh/hello-dagger-rs-{}:1h", rng.gen::<u64>()),
            None,
        )?;

    println!("published image to: {}", ref_);

    Ok(())
}
