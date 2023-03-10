use std::sync::Arc;

use clap::ArgMatches;
use dagger_sdk::{Container, HostDirectoryOpts, Query};

#[tokio::main]
async fn main() -> eyre::Result<()> {
    color_eyre::install().unwrap();

    let matches = clap::Command::new("ci")
        .subcommand_required(true)
        .subcommand(clap::Command::new("pr"))
        .subcommand(clap::Command::new("release"))
        .subcommand(clap::Command::new("codegen"))
        .get_matches();

    let client = dagger_sdk::connect().await?;

    match matches.subcommand() {
        Some(("pr", _)) => {
            let base = select_base_image(client.clone()).await?;
            return validate_pr(client, base).await;
        }
        Some(("release", subm)) => return release(client, subm).await,
        Some(("codegen", subm)) => return run_codegen(client, subm).await,
        Some(_) => {
            panic!("invalid subcommand selected!")
        }
        None => {
            panic!("no command selected!")
        }
    }
}

async fn run_codegen(client: Arc<Query>, _subm: &ArgMatches) -> eyre::Result<()> {
    let docker_cli = client
        .container()
        .from("docker:cli")
        .file("/usr/local/bin/docker");
    let socket = client.host().unix_socket("/var/run/docker.sock");

    let container = get_dependencies(client).await?;

    let generated_image = container
        .with_exec(vec!["mkdir", "-p", "/mnt/output"])
        .with_mounted_file("/usr/bin/docker", docker_cli.id().await?)
        .with_unix_socket("/var/run/docker.sock", socket.id().await?)
        .with_exec(vec![
            "cargo",
            "run",
            "--",
            "generate",
            "--output",
            "crates/dagger-sdk/gen.rs",
        ])
        .with_exec(vec!["cargo", "fmt", "--all"])
        .with_exec(vec!["cargo", "fix", "--workspace", "--allow-dirty"])
        .with_exec(vec!["cargo", "fmt", "--all"])
        .with_exec(vec!["mv", "crates/dagger-sdk/gen.rs", "/mnt/output/gen.rs"]);

    let _ = generated_image.exit_code().await?;

    generated_image
        .file("/mnt/output/gen.rs")
        .export("crates/dagger-sdk/src/gen.rs")
        .await?;

    Ok(())
}

async fn release(client: Arc<Query>, _subm: &clap::ArgMatches) -> Result<(), color_eyre::Report> {
    let src_dir = client.host().directory_opts(
        ".",
        HostDirectoryOpts {
            exclude: Some(vec!["target/"]),
            include: None,
        },
    );
    let base_image = client
        .container()
        .from("rust:latest")
        .with_workdir("app")
        .with_mounted_directory("/app/", src_dir.id().await?);

    let container = base_image
        .with_exec(vec!["cargo", "install", "cargo-smart-release"])
        .with_exec(vec![
            "cargo",
            "smart-release",
            "--execute",
            "--allow-fully-generated-changelogs",
            "--no-changelog-preview",
            "dagger-rs",
            "dagger-sdk",
        ]);
    let exit = container.exit_code().await?;
    if exit != 0 {
        eyre::bail!("container failed with non-zero exit code");
    }

    println!("released pr succeeded!");

    Ok(())
}

async fn get_dependencies(client: Arc<Query>) -> eyre::Result<Container> {
    let cargo_dir = client.host().directory_opts(
        ".",
        HostDirectoryOpts {
            exclude: None,
            include: Some(vec![
                "**/Cargo.lock",
                "**/Cargo.toml",
                "**/main.rs",
                "**/lib.rs",
            ]),
        },
    );

    let src_dir = client.host().directory_opts(
        ".",
        HostDirectoryOpts {
            exclude: Some(vec!["target/"]),
            include: None,
        },
    );

    let cache_cargo_index_dir = client.cache_volume("cargo_index");
    let _cache_cargo_deps = client.cache_volume("cargo_deps");
    let cache_cargo_bin = client.cache_volume("cargo_bin_cache");

    let minio_url = "https://github.com/mozilla/sccache/releases/download/v0.3.3/sccache-v0.3.3-x86_64-unknown-linux-musl.tar.gz";

    let base_image = client
        .container()
        .from("rustlang/rust:nightly")
        .with_workdir("app")
        .with_exec(vec!["apt-get", "update"])
        .with_exec(vec!["apt-get", "install", "--yes", "libpq-dev", "wget"])
        .with_exec(vec!["wget", minio_url])
        .with_exec(vec![
            "tar",
            "xzf",
            "sccache-v0.3.3-x86_64-unknown-linux-musl.tar.gz",
        ])
        .with_exec(vec![
            "mv",
            "sccache-v0.3.3-x86_64-unknown-linux-musl/sccache",
            "/usr/local/bin/sccache",
        ])
        .with_exec(vec!["chmod", "+x", "/usr/local/bin/sccache"])
        //.with_env_variable("RUSTC_WRAPPER", "/usr/local/bin/sccache")
        .with_env_variable(
            "AWS_ACCESS_KEY_ID",
            std::env::var("AWS_ACCESS_KEY_ID").unwrap_or("".into()),
        )
        .with_env_variable(
            "AWS_SECRET_ACCESS_KEY",
            std::env::var("AWS_SECRET_ACCESS_KEY").unwrap_or("".into()),
        )
        .with_env_variable("SCCACHE_BUCKET", "sccache")
        .with_env_variable("SCCACHE_REGION", "auto")
        .with_env_variable("SCCACHE_ENDPOINT", "https://api-minio.front.kjuulh.io")
        .with_mounted_cache("~/.cargo/bin", cache_cargo_bin.id().await?)
        .with_mounted_cache("~/.cargo/registry/index", cache_cargo_bin.id().await?)
        .with_mounted_cache("~/.cargo/registry/cache", cache_cargo_bin.id().await?)
        .with_mounted_cache("~/.cargo/git/db", cache_cargo_bin.id().await?)
        .with_mounted_cache("target/", cache_cargo_bin.id().await?)
        .with_exec(vec!["cargo", "install", "cargo-chef"]);

    let recipe = base_image
        .with_mounted_directory(".", cargo_dir.id().await?)
        .with_mounted_cache("~/.cargo/.package-cache", cache_cargo_index_dir.id().await?)
        .with_exec(vec![
            "cargo",
            "chef",
            "prepare",
            "--recipe-path",
            "recipe.json",
        ])
        .file("/app/recipe.json");

    let builder_start = base_image
        .with_mounted_file("/app/recipe.json", recipe.id().await?)
        .with_exec(vec![
            "cargo",
            "chef",
            "cook",
            "--release",
            "--workspace",
            "--recipe-path",
            "recipe.json",
        ])
        .with_directory("/app/", src_dir.id().await?)
        .with_exec(vec!["cargo", "build", "--all", "--release"]);

    return Ok(builder_start);
}

async fn select_base_image(client: Arc<Query>) -> eyre::Result<Container> {
    let src_dir = get_dependencies(client.clone()).await;

    src_dir
}

async fn validate_pr(client: Arc<Query>, container: Container) -> eyre::Result<()> {
    let exit = container.exit_code().await?;
    if exit != 0 {
        eyre::bail!("container failed with non-zero exit code");
    }
    let docker_cli = client
        .container()
        .from("docker:cli")
        .file("/usr/local/bin/docker");
    let socket = client.host().unix_socket("/var/run/docker.sock");

    let container = container
        .with_mounted_file("/usr/bin/docker", docker_cli.id().await?)
        .with_unix_socket("/var/run/docker.sock", socket.id().await?)
        .with_exec(vec!["cargo", "test", "--all"]);

    let exit = container.exit_code().await?;
    if exit != 0 {
        eyre::bail!("container failed with non-zero exit code");
    }

    println!("validating pr succeeded!");

    Ok(())
}
