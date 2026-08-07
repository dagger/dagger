//! Engine-backed tests retained for execution when an external Dagger session is
//! supplied. The stable client deliberately does not launch an implicit CLI session
//! until that connector exists, so these are not part of the default workspace gate.

use std::time::{Duration, SystemTime};

use dagger_sdk::{Client, ClientConfig, QueryError, connect, connect_with};
use pretty_assertions::assert_eq;

mod support;

#[test]
fn test_foundations_record_boundaries_and_persist_256_cases() {
    use support::{
        EventLog, PROPTEST_CASES, RecordedAction, RecordingConnection, RecordingConnector,
        RecordingResource, proptest_config,
    };

    let events = EventLog::default();
    let connection = RecordingConnection(events.clone());
    let connector = RecordingConnector(events.clone());
    let resource = RecordingResource(events.clone());
    connector.connect();
    connection.execute();
    connection.close();
    connection.abort();
    resource.close();

    assert_eq!(
        events.actions(),
        [
            RecordedAction::ConnectorConnect,
            RecordedAction::ConnectionExecute,
            RecordedAction::ConnectionClose,
            RecordedAction::ConnectionAbort,
            RecordedAction::ResourceClose,
        ]
    );
    let config = proptest_config();
    assert_eq!(config.cases, PROPTEST_CASES);
    assert!(config.failure_persistence.is_some());
}

async fn connected() -> Client {
    connect().await.expect("test session should be available")
}

#[tokio::test]
#[ignore = "requires an externally provided Dagger engine session"]
async fn test_error_parsing() {
    let client = connected().await;
    let err = client
        .query()
        .container()
        .from("alpine:3.16.2")
        .with_exec(vec!["/bin/sh", "-c", "echo test; exit 1"])
        .stdout()
        .await
        .expect_err("command should fail");

    let QueryError::GraphQl { response } = err else {
        panic!("expected a GraphQL error response");
    };
    let error = response.errors().first().expect("engine error");
    let extensions = error.extensions().expect("exec metadata");
    assert!(
        extensions
            .values()
            .any(|value| value.to_string().contains("exit"))
    );
    client.close().await.expect("close test session");
}

#[tokio::test]
#[ignore = "requires an externally provided Dagger engine session"]
async fn test_execute_timeout() {
    let short = ClientConfig::builder()
        .graphql_execution_timeout(Duration::from_millis(600))
        .build()
        .expect("valid timeout");
    let client = connect_with(short).await.expect("connect with timeout");
    let result = client
        .query()
        .container()
        .from("alpine:3.16.2")
        .with_env_variable("CACHE_BUSTER", unique_value())
        .with_exec(vec!["sleep", "1"])
        .stdout()
        .await;
    assert!(result.is_err());
    client.close().await.expect("close timed-out session");

    let long = ClientConfig::builder()
        .graphql_execution_timeout(Duration::from_secs(600))
        .build()
        .expect("valid timeout");
    let client = connect_with(long).await.expect("connect with timeout");
    client
        .query()
        .container()
        .from("alpine:3.16.2")
        .with_env_variable("CACHE_BUSTER", unique_value())
        .with_exec(vec!["sleep", "1"])
        .stdout()
        .await
        .expect("long timeout should permit request");
    client.close().await.expect("close test session");
}

fn unique_value() -> String {
    SystemTime::now()
        .duration_since(SystemTime::UNIX_EPOCH)
        .expect("system clock after epoch")
        .as_millis()
        .to_string()
}

#[tokio::test]
#[ignore = "requires an externally provided Dagger engine session"]
async fn test_default_config_connects() {
    let client = connected().await;
    client
        .query()
        .container()
        .from("alpine:3.16.2")
        .with_exec(vec!["/bin/true"])
        .stdout()
        .await
        .expect("default config request");
    client.close().await.expect("close test session");
}

#[tokio::test]
#[ignore = "requires an externally provided Dagger engine session"]
async fn test_example_container() {
    let client = connected().await;
    let out = client
        .query()
        .container()
        .from("alpine:3.16.2")
        .with_exec(vec!["cat", "/etc/alpine-release"])
        .stdout()
        .await
        .expect("container output");
    assert_eq!(out, "3.16.2\n");
    client.close().await.expect("close test session");
}

#[tokio::test]
#[ignore = "requires an externally provided Dagger engine session"]
async fn test_directory() {
    let client = connected().await;
    let contents = client
        .query()
        .directory()
        .with_new_file("/hello.txt", "world")
        .file("/hello.txt")
        .contents()
        .await
        .expect("file contents");
    assert_eq!(contents, "world");
    client.close().await.expect("close test session");
}

#[tokio::test]
#[ignore = "requires an externally provided Dagger engine session"]
async fn test_git() {
    let client = connected().await;
    let tree = client
        .query()
        .git("github.com/dagger/dagger")
        .branch("main")
        .tree();
    let entries = tree.entries().await.expect("git entries");
    assert!(entries.iter().any(|entry| entry == "README.md"));
    let readme = tree
        .file("README.md")
        .contents()
        .await
        .expect("readme contents");
    assert!(readme.contains("Dagger"));
    client.close().await.expect("close test session");
}

#[tokio::test]
#[ignore = "requires an externally provided Dagger engine session"]
async fn test_container() {
    let client = connected().await;
    let alpine = client.query().container().from("alpine:3.16.2");
    let contents = alpine
        .file("/etc/alpine-release")
        .contents()
        .await
        .expect("file contents");
    assert_eq!(contents, "3.16.2\n");
    let out = alpine
        .with_exec(vec!["cat", "/etc/alpine-release"])
        .stdout()
        .await
        .expect("command output");
    assert_eq!(out, "3.16.2\n");
    client.close().await.expect("close test session");
}

#[tokio::test]
#[ignore = "requires an externally provided Dagger engine session"]
async fn test_node_load_container() {
    let client = connected().await;
    let query = client.query();
    let id = query
        .container()
        .from("alpine:3.16.2")
        .id()
        .await
        .expect("container ID");
    let loaded: dagger_sdk::Container = query.r#ref(id);
    let out = loaded
        .with_exec(vec!["cat", "/etc/alpine-release"])
        .stdout()
        .await
        .expect("loaded container output");
    assert_eq!(out, "3.16.2\n");
    client.close().await.expect("close test session");
}

#[tokio::test]
#[ignore = "requires an externally provided Dagger engine session"]
async fn test_node_load_directory() {
    let client = connected().await;
    let query = client.query();
    let id = query
        .directory()
        .with_new_file("/hello.txt", "world")
        .id()
        .await
        .expect("directory ID");
    let loaded: dagger_sdk::Directory = query.r#ref(id);
    let contents = loaded
        .file("/hello.txt")
        .contents()
        .await
        .expect("loaded directory contents");
    assert_eq!(contents, "world");
    client.close().await.expect("close test session");
}

#[tokio::test]
#[ignore = "requires an externally provided Dagger engine session"]
async fn test_node_load_file() {
    let client = connected().await;
    let query = client.query();
    let id = query
        .directory()
        .with_new_file("/hello.txt", "from-id")
        .file("/hello.txt")
        .id()
        .await
        .expect("file ID");
    let loaded: dagger_sdk::File = query.r#ref(id);
    assert_eq!(
        loaded.contents().await.expect("loaded file contents"),
        "from-id"
    );
    client.close().await.expect("close test session");
}

#[tokio::test]
#[ignore = "requires an externally provided Dagger engine session"]
async fn test_container_sync_roundtrip() {
    let client = connected().await;
    let synced = client
        .query()
        .container()
        .from("alpine:3.16.2")
        .sync()
        .await
        .expect("synced container");
    let out = synced
        .with_exec(vec!["cat", "/etc/alpine-release"])
        .stdout()
        .await
        .expect("synced output");
    assert_eq!(out, "3.16.2\n");
    client.close().await.expect("close test session");
}

#[tokio::test]
#[ignore = "requires an externally provided Dagger engine session"]
async fn test_env_variables() {
    let client = connected().await;
    let envs = client
        .query()
        .container()
        .from("alpine:3.20.2")
        .with_env_variable("FOO", "bar")
        .env_variables()
        .await
        .expect("environment handles");
    let names = futures::future::try_join_all(envs.iter().map(|env| env.name()))
        .await
        .expect("environment names");
    assert_eq!(names, vec!["PATH".to_owned(), "FOO".to_owned()]);
    client.close().await.expect("close test session");
}
