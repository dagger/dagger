use std::sync::Arc;

use dagger_sdk::gen::{Container, HostDirectoryOpts, Query};

fn main() -> eyre::Result<()> {
    color_eyre::install().unwrap();

    let matches = clap::Command::new("ci")
        .subcommand_required(true)
        .subcommand(clap::Command::new("pr"))
        .get_matches();

    let client = dagger_sdk::client::connect()?;

    let base = select_base_image(client.clone());

    match matches.subcommand() {
        Some(("pr", _)) => return validate_pr(client, base),
        Some(_) => {
            panic!("invalid subcommand selected!")
        }
        None => {
            panic!("no command selected!")
        }
    }
}

fn get_dependencies(client: Arc<Query>) -> Container {
    let cargo_dir = client.host().directory(
        ".".into(),
        Some(HostDirectoryOpts {
            exclude: None,
            include: Some(vec![
                "**/Cargo.lock".into(),
                "**/Cargo.toml".into(),
                "**/main.rs".into(),
                "**/lib.rs".into(),
            ]),
        }),
    );

    let src_dir = client.host().directory(
        ".".into(),
        Some(HostDirectoryOpts {
            exclude: Some(vec!["target/".into()]),
            include: None,
        }),
    );

    let cache_cargo_index_dir = client.cache_volume("cargo_index".into());
    let cache_cargo_deps = client.cache_volume("cargo_deps".into());

    let base_image = client
        .container(None)
        .from("rust:latest".into())
        .with_workdir("app".into())
        .with_exec(
            vec!["cargo".into(), "install".into(), "cargo-chef".into()],
            None,
        );

    let recipe = base_image
        .with_mounted_directory(".".into(), cargo_dir.id())
        .with_mounted_cache(
            "~/.cargo/.package-cache".into(),
            cache_cargo_index_dir.id(),
            None,
        )
        .with_exec(
            vec![
                "cargo".into(),
                "chef".into(),
                "prepare".into(),
                "--recipe-path".into(),
                "recipe.json".into(),
            ],
            None,
        )
        .file("/app/recipe.json".into());

    let builder_start = base_image
        .with_mounted_file("/app/recipe.json".into(), recipe.id())
        .with_exec(
            vec![
                "cargo".into(),
                "chef".into(),
                "cook".into(),
                "--release".into(),
                "--recipe-path".into(),
                "recipe.json".into(),
            ],
            None,
        )
        .with_mounted_cache("/app/".into(), cache_cargo_deps.id(), None)
        .with_mounted_directory("/app/".into(), src_dir.id())
        .with_exec(
            vec![
                "cargo".into(),
                "build".into(),
                "--all".into(),
                "--release".into(),
            ],
            None,
        );

    return builder_start;
}

fn select_base_image(client: Arc<Query>) -> Container {
    let src_dir = get_dependencies(client.clone());

    src_dir
}

fn validate_pr(_client: Arc<Query>, container: Container) -> eyre::Result<()> {
    //let container = container.with_exec(vec!["cargo".into(), "test".into(), "--all".into()], None);

    let exit = container.exit_code();
    if exit != 0 {
        eyre::bail!("container failed with non-zero exit code");
    }

    println!("validating pr succeeded!");

    Ok(())
}
