//! Focused generated-client observations against the checked Dagger engine target.

use std::error::Error;
use std::time::Duration;

use async_trait::async_trait;
use dagger_sdk::{
    BuildArg, CacheSharingMode, ClientConfig, DirectoryDockerBuildOpts, EngineConnection,
    EngineConnectionError, EngineConnectionErrorKind, QueryCacheVolumeOpts, QueryError,
    QueryGitOpts, RawRequest, RawResponse, RequestError, ResponseData, Syncer,
};
use serde::Serialize;
use serde_json::json;

const TARGET_REVISION: &str = "25300124ca110612edc09c43f89cb5fad6028170";
const TARGET_VERSION: &str = "v1.0.0-beta.10";

#[derive(Serialize)]
struct Observation {
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

enum ProbeMode {
    Transport,
    Decode,
}

struct ProbeConnection(ProbeMode);

#[async_trait]
impl EngineConnection for ProbeConnection {
    async fn execute(&self, _request: RawRequest) -> Result<RawResponse, EngineConnectionError> {
        match self.0 {
            ProbeMode::Transport => Err(EngineConnectionError::new(
                EngineConnectionErrorKind::Transport,
            )),
            ProbeMode::Decode => Ok(RawResponse::new(ResponseData::Value(json!({
                "version": 42
            })))),
        }
    }

    async fn close(&self) -> Result<(), EngineConnectionError> {
        Ok(())
    }

    fn abort(&self) {}
}

async fn probe_client(mode: ProbeMode) -> Result<dagger_sdk::Client, Box<dyn Error>> {
    let config = ClientConfig::builder()
        .connection(Box::new(ProbeConnection(mode)))
        .build()?;
    Ok(dagger_sdk::connect_with(config).await?)
}

async fn interface_id(value: &impl Syncer) -> Result<dagger_sdk::Id, QueryError> {
    Syncer::id(value).await
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    let client = dagger_sdk::connect().await?;
    let query = client.query();

    let version = query.version().await?;
    assert!(version.starts_with(TARGET_VERSION));
    let _platform = query.default_platform().await?;

    let enum_opts = QueryCacheVolumeOpts::default().with_sharing(CacheSharingMode::Private);
    let _cache_id = query
        .cache_volume_opts("rust-core-conformance-enum", &enum_opts)
        .id()
        .await?;

    let dockerfile = "FROM alpine:3.22\nARG MESSAGE\nRUN test \"$MESSAGE\" = input-object\n";
    let source = query
        .directory()
        .with_new_file(dockerfile.to_owned(), "Dockerfile");
    let build_opts = DirectoryDockerBuildOpts::default().with_build_args(vec![BuildArg::new(
        "MESSAGE".to_owned(),
        "input-object".to_owned(),
    )]);
    let built = source.docker_build_opts(&build_opts);
    assert_eq!(
        built
            .with_exec(vec![
                "sh".to_owned(),
                "-c".to_owned(),
                "printf input-object".to_owned()
            ])
            .stdout()
            .await?,
        "input-object"
    );

    let lazy = query.container().from("alpine:3.22");
    let _interface_id = interface_id(&lazy).await?;
    assert!(lazy.docker_healthcheck().await?.is_none());

    let envs = lazy
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

    let lazy_id = lazy.id().await?;
    assert!(query.node(lazy_id).await?.is_some());

    let handle_opts = QueryCacheVolumeOpts::default().with_source(
        query
            .directory()
            .with_new_file("retained".to_owned(), "handle-source")
            .into(),
    );
    let _handle_id = query
        .cache_volume_opts("rust-core-conformance-handle", &handle_opts)
        .id()
        .await?;

    let synced = lazy.sync().await?;
    let _synced_id = synced.id().await?;

    let git_opts = QueryGitOpts::default().with_keep_git_dir(false);
    let commit = query
        .git_opts("https://github.com/octocat/Hello-World.git", &git_opts)
        .head()
        .commit()
        .await?;
    assert!(!commit.is_empty());

    let graphql_error = query
        .directory()
        .with_error("rust core conformance GraphQL error")
        .entries()
        .await
        .expect_err("withError must surface a GraphQL failure");
    assert!(matches!(graphql_error, QueryError::GraphQl { .. }));

    let exec_error = query
        .container()
        .from("alpine:3.22")
        .with_exec(vec!["sh".to_owned(), "-c".to_owned(), "exit 17".to_owned()])
        .stdout()
        .await
        .expect_err("non-zero process exit must surface an engine-domain error");
    assert!(matches!(exec_error, QueryError::Exec { .. }));

    let timeout_config = ClientConfig::builder()
        .graphql_execution_timeout(Duration::from_millis(250))
        .build()?;
    let timeout_client = dagger_sdk::connect_with(timeout_config).await?;
    let timeout_error = timeout_client
        .query()
        .container()
        .from("alpine:3.22")
        .with_exec(vec!["sh".to_owned(), "-c".to_owned(), "sleep 2".to_owned()])
        .stdout()
        .await
        .expect_err("the complete-request timeout must fence a slow generated call");
    assert!(matches!(
        timeout_error,
        QueryError::Request(RequestError::ExecutionTimeout { .. })
    ));
    timeout_client.close().await?;

    let transport_client = probe_client(ProbeMode::Transport).await?;
    let transport_error = transport_client
        .query()
        .version()
        .await
        .expect_err("the injected transport failure must remain typed");
    assert!(matches!(
        transport_error,
        QueryError::Request(RequestError::Connection(ref error))
            if error.kind() == EngineConnectionErrorKind::Transport
    ));
    transport_client.close().await?;

    let decode_client = probe_client(ProbeMode::Decode).await?;
    let decode_error = decode_client
        .query()
        .version()
        .await
        .expect_err("the incompatible response value must fail generated decoding");
    assert!(matches!(decode_error, QueryError::Decode(_)));
    decode_client.close().await?;

    query.engine().local_cache().prune().await?;

    client.close().await?;
    let close_error = query
        .version()
        .await
        .expect_err("generated calls after close must be fenced locally");
    assert!(matches!(
        close_error,
        QueryError::Request(RequestError::ClientClosed)
    ));

    let observations = vec![
        Observation {
            category: "scalar",
            operation: "Query.version",
        },
        Observation {
            category: "custom-scalar",
            operation: "Query.defaultPlatform",
        },
        Observation {
            category: "enum",
            operation: "Query.cacheVolume(sharing:)",
        },
        Observation {
            category: "input-object",
            operation: "Directory.dockerBuild(buildArgs:)",
        },
        Observation {
            category: "lazy-object",
            operation: "Query.container",
        },
        Observation {
            category: "interface",
            operation: "Container.id",
        },
        Observation {
            category: "nullable-handle",
            operation: "Container.dockerHealthcheck",
        },
        Observation {
            category: "object-list",
            operation: "Container.envVariables",
        },
        Observation {
            category: "expected-type-raw-id",
            operation: "Query.node(id:)",
        },
        Observation {
            category: "expected-type-handle",
            operation: "Query.cacheVolume(source:)",
        },
        Observation {
            category: "self-reentry",
            operation: "Container.sync",
        },
        Observation {
            category: "void",
            operation: "EngineCache.prune",
        },
        Observation {
            category: "explicit-zero-like",
            operation: "Query.git(keepGitDir:)",
        },
        Observation {
            category: "close",
            operation: "Query.version",
        },
        Observation {
            category: "timeout",
            operation: "Container.stdout",
        },
        Observation {
            category: "transport-error",
            operation: "Query.version",
        },
        Observation {
            category: "graphql-error",
            operation: "Directory.entries",
        },
        Observation {
            category: "engine-error",
            operation: "Container.stdout",
        },
        Observation {
            category: "decode-error",
            operation: "Query.version",
        },
    ];
    let encoded = serde_json::to_vec(&ObservationSet {
        format_version: 1,
        target_revision: TARGET_REVISION,
        target_version: TARGET_VERSION,
        observations,
    })?;
    if let Some(path) = std::env::var_os("DAGGER_RUST_CONFORMANCE_OUTPUT") {
        std::fs::write(path, encoded)?;
    } else {
        println!("{}", String::from_utf8(encoded)?);
    }

    Ok(())
}
