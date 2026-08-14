// One-selector generated-client observations against the checked Dagger engine target.

use std::error::Error;

use dagger_sdk::{
    BuildArg, CacheSharingMode, DirectoryDockerBuildOpts, QueryCacheVolumeOpts, QueryError,
    QueryGitOpts, Syncer,
};
use serde::Serialize;

const TARGET_REVISION: &str = "25300124ca110612edc09c43f89cb5fad6028170";
const TARGET_VERSION: &str = "v1.0.0-beta.10";

#[derive(Serialize)]
struct Observation {
    selector: String,
    category: &'static str,
    operation: &'static str,
}

#[derive(Serialize)]
struct ObservationSet {
    format_version: u32,
    target_revision: &'static str,
    target_version: &'static str,
    observations: Vec<Observation>,
}

async fn interface_id(value: &impl Syncer) -> Result<dagger_sdk::Id, QueryError> {
    Syncer::id(value).await
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    let selector = std::env::var("DAGGER_RUST_SIGNOFF_SELECTOR")
        .map_err(|_| "DAGGER_RUST_SIGNOFF_SELECTOR is required")?;
    let client = dagger_sdk::connect().await?;
    let query = client.query();

    // Each selector owns its assertion block. Running the complete fixture for every catalog row
    // would couple independent outcomes and obscure the exact API surface that actually failed.
    let (category, operation) = match selector.as_str() {
        "scalar" => {
            let version = query.version().await?;
            assert!(version.starts_with(TARGET_VERSION));
            ("scalar", "Query.version")
        }
        "enum" => {
            let opts = QueryCacheVolumeOpts::default().with_sharing(CacheSharingMode::Private);
            let _id = query
                .cache_volume_opts("rust-core-conformance-enum", &opts)
                .id()
                .await?;
            ("enum", "Query.cacheVolume(sharing:)")
        }
        "input" | "directory" => {
            let dockerfile =
                "FROM alpine:3.22\nARG MESSAGE\nRUN test \"$MESSAGE\" = input-object\n";
            let source = query
                .directory()
                .with_new_file(dockerfile.to_owned(), "Dockerfile");
            let opts = DirectoryDockerBuildOpts::default().with_build_args(vec![BuildArg::new(
                "MESSAGE".to_owned(),
                "input-object".to_owned(),
            )]);
            let stdout = source
                .docker_build_opts(&opts)
                .with_exec(vec![
                    "sh".to_owned(),
                    "-c".to_owned(),
                    "printf input-object".to_owned(),
                ])
                .stdout()
                .await?;
            assert_eq!(stdout, "input-object");
            ("input-object", "Directory.dockerBuild(buildArgs:)")
        }
        "object" | "container" => {
            let _id = query.container().from("alpine:3.22").id().await?;
            ("lazy-object", "Query.container")
        }
        "interface" => {
            let container = query.container().from("alpine:3.22");
            let _id = interface_id(&container).await?;
            ("interface", "Container.id")
        }
        "nullable" => {
            assert!(
                query
                    .container()
                    .from("alpine:3.22")
                    .docker_healthcheck()
                    .await?
                    .is_none()
            );
            ("nullable-handle", "Container.dockerHealthcheck")
        }
        "list-object" | "list" => {
            let envs = query
                .container()
                .from("alpine:3.22")
                .with_env_variable("RUST_CONFORMANCE_FIRST", "one")
                .with_env_variable("RUST_CONFORMANCE_SECOND", "two")
                .env_variables()
                .await?;
            let mut names = Vec::with_capacity(envs.len());
            for env in envs {
                names.push(env.name().await?);
            }
            let first = names
                .iter()
                .position(|name| name == "RUST_CONFORMANCE_FIRST")
                .expect("first environment value must be retained");
            let second = names
                .iter()
                .position(|name| name == "RUST_CONFORMANCE_SECOND")
                .expect("second environment value must be retained");
            assert!(first < second);
            ("object-list", "Container.envVariables")
        }
        "expected-type" => {
            let container = query.container().from("alpine:3.22");
            let id = container.id().await?;
            assert!(query.node(id).await?.is_some());
            ("expected-type-raw-id", "Query.node(id:)")
        }
        "void" => {
            query.engine().local_cache().prune().await?;
            ("void", "EngineCache.prune")
        }
        "git" => {
            let opts = QueryGitOpts::default().with_keep_git_dir(false);
            let commit = query
                .git_opts("https://github.com/octocat/Hello-World.git", &opts)
                .head()
                .commit()
                .await?;
            assert!(!commit.is_empty());
            ("explicit-zero-like", "Query.git(keepGitDir:)")
        }
        "container-mutation" => {
            let value = query
                .container()
                .from("alpine:3.22")
                .with_env_variable("RUST_CONFORMANCE_MUTATION", "retained")
                .env_variable("RUST_CONFORMANCE_MUTATION")
                .await?;
            assert_eq!(value.as_deref(), Some("retained"));
            ("object-mutation", "Container.withEnvVariable")
        }
        "typed-exec-error" => {
            let error = failing_exec(&query).await;
            assert!(matches!(error, QueryError::Exec { .. }));
            ("engine-error", "Container.stdout")
        }
        "exec-error-output-fields" => {
            let error = failing_exec(&query).await;
            match error {
                QueryError::Exec { error, .. } => {
                    assert_eq!(error.exit_code(), Some(17));
                    assert_eq!(error.stdout(), Some("rust-stdout"));
                    assert_eq!(error.stderr(), Some("rust-stderr"));
                    assert_eq!(
                        error.command(),
                        Some(
                            [
                                "sh".to_owned(),
                                "-c".to_owned(),
                                "printf rust-stdout; printf rust-stderr >&2; exit 17".to_owned(),
                            ]
                            .as_slice(),
                        ),
                    );
                }
                other => panic!("non-zero process exit returned {other:?}"),
            }
            ("engine-error-fields", "Container.stdout")
        }
        "exec-error-empty-output" => {
            let error = match query
                .container()
                .from("alpine:3.22")
                .with_exec(vec!["false".to_owned()])
                .sync()
                .await
            {
                Ok(_) => panic!("empty execution failure unexpectedly passed"),
                Err(error) => error,
            };
            match error {
                QueryError::Exec { error, .. } => {
                    assert_eq!(error.exit_code(), Some(1));
                    assert_eq!(error.stdout(), Some(""));
                    assert_eq!(error.stderr(), Some(""));
                }
                other => panic!("empty execution failure returned {other:?}"),
            }
            ("engine-error-empty-output", "Container.sync")
        }
        "non-exec-error-separation" => {
            let error = query
                .directory()
                .with_error("rust core conformance GraphQL error")
                .entries()
                .await
                .expect_err("withError must surface a GraphQL failure");
            assert!(matches!(error, QueryError::GraphQl { .. }));
            ("graphql-error", "Directory.entries")
        }
        other => {
            return Err(
                std::io::Error::other(format!("unknown Rust sign-off selector {other:?}")).into(),
            );
        }
    };

    client.close().await?;
    let encoded = serde_json::to_vec(&ObservationSet {
        format_version: 1,
        target_revision: TARGET_REVISION,
        target_version: TARGET_VERSION,
        observations: vec![Observation {
            selector,
            category,
            operation,
        }],
    })?;
    println!("{}", String::from_utf8(encoded)?);
    Ok(())
}

async fn failing_exec(query: &dagger_sdk::Query) -> QueryError {
    query
        .container()
        .from("alpine:3.22")
        .with_exec(vec![
            "sh".to_owned(),
            "-c".to_owned(),
            "printf rust-stdout; printf rust-stderr >&2; exit 17".to_owned(),
        ])
        .stdout()
        .await
        .expect_err("non-zero process exit must surface an engine-domain error")
}
