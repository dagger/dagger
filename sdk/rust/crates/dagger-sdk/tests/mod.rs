use dagger_sdk::{
    connect, connect_opts,
    core::{config, gql_client::GraphQlExtension, graphql_client::GraphQLError},
    errors::DaggerError,
    logging::StdLogger,
};
use pretty_assertions::assert_eq;
use std::sync::Arc;

#[tokio::test]
async fn test_error_parsing() {
    connect(|client| async move {
        let alpine = client.container().from("alpine:3.16.2");

        let err = alpine
            .with_exec(vec!["/bin/sh", "-c", "echo test; exit 1"])
            .stdout()
            .await
            .expect_err("should return an error");

        let DaggerError::Query(GraphQLError::DomainError { fields, .. }) = err else {
            panic!("should be a query error");
        };

        let GraphQlExtension::ExecError {
            cmd,
            exit_code,
            stderr,
            stdout,
        } = fields
            .first()
            .expect("should be an exec error")
            .extensions
            .as_ref()
            .expect("should have an extension")
        else {
            panic!("should be an exec error");
        };

        assert_eq!(
            cmd,
            &vec![
                "/bin/sh".to_string(),
                "-c".to_string(),
                "echo test; exit 1".to_string()
            ]
        );
        assert_eq!(exit_code, &1);
        assert_eq!(stdout, &"test");
        assert_eq!(stderr, &"");

        Ok(())
    })
    .await
    .expect("should succeed");
}

#[tokio::test]
async fn test_execute_timeout() {
    use std::time::SystemTime;

    let config = config::Config::builder().execute_timeout_ms(600).build();
    connect_opts(config, |client| async move {
        let alpine = client.container().from("alpine:3.16.2");

        alpine
            .with_env_variable(
                "CACHE_BUSTER",
                SystemTime::now()
                    .duration_since(SystemTime::UNIX_EPOCH)
                    .unwrap()
                    .as_millis()
                    .to_string(),
            )
            .with_exec(vec!["sleep", "1"])
            .stdout()
            .await?;

        Ok(())
    })
    .await
    .expect_err("should timeout");

    let config = config::Config::builder().execute_timeout_ms(600000).build();
    connect_opts(config, |client| async move {
        let alpine = client.container().from("alpine:3.16.2");

        alpine
            .with_env_variable(
                "CACHE_BUSTER",
                SystemTime::now()
                    .duration_since(SystemTime::UNIX_EPOCH)
                    .unwrap()
                    .as_millis()
                    .to_string(),
            )
            .with_exec(vec!["sleep", "1"])
            .stdout()
            .await?;

        Ok(())
    })
    .await
    .expect("should not timeout");
}

#[tokio::test]
async fn test_default_config_connects() {
    let mut cfg = config::Config::default();
    assert_eq!(
        cfg.timeout_ms,
        10 * 1000,
        "Config::default should keep 10s timeout"
    );
    cfg.logger = Some(Arc::new(StdLogger::default()));

    connect_opts(cfg, |client| async move {
        client
            .container()
            .from("alpine:3.16.2")
            .with_exec(vec!["/bin/true"])
            .stdout()
            .await?;

        Ok(())
    })
    .await
    .expect("default config should connect with non-zero timeout");
}

#[tokio::test]
async fn test_example_container() {
    connect(|client| async move {
        let alpine = client.container().from("alpine:3.16.2");

        let out = alpine
            .with_exec(vec!["cat", "/etc/alpine-release"])
            .stdout()
            .await
            .unwrap();

        assert_eq!(out, "3.16.2\n".to_string());

        Ok(())
    })
    .await
    .unwrap();
}

#[tokio::test]
async fn test_directory() {
    connect(|client| async move {
        let contents = client
            .directory()
            .with_new_file("/hello.txt", "world")
            .file("/hello.txt")
            .contents()
            .await
            .unwrap();

        assert_eq!("world", contents);

        Ok(())
    })
    .await
    .unwrap();
}

#[tokio::test]
async fn test_git() {
    connect(|client| async move {
        let tree = client.git("github.com/dagger/dagger").branch("main").tree();

        let _ = tree
            .entries()
            .await
            .unwrap()
            .iter()
            .find(|f| f.as_str() == "README.md")
            .unwrap();

        let readme_file = tree.file("README.md");

        let readme = readme_file.contents().await.unwrap();
        assert_eq!(true, readme.contains("Dagger"));

        let other_readme = client
            .load_file_from_id(readme_file)
            .contents()
            .await
            .unwrap();

        assert_eq!(readme, other_readme);

        Ok(())
    })
    .await
    .unwrap();
}

#[tokio::test]
async fn test_container() {
    connect(|client| async move {
        let alpine = client.container().from("alpine:3.16.2");

        let contents = alpine.file("/etc/alpine-release").contents().await.unwrap();
        assert_eq!(contents, "3.16.2\n".to_string());

        let out = alpine
            .with_exec(vec!["cat", "/etc/alpine-release"])
            .stdout()
            .await
            .unwrap();
        assert_eq!(out, "3.16.2\n".to_string());

        let id = alpine.id().await.unwrap();
        let contents = client
            .load_container_from_id(id)
            .file("/etc/alpine-release")
            .contents()
            .await
            .unwrap();
        assert_eq!(contents, "3.16.2\n".to_string());

        Ok(())
    })
    .await
    .unwrap();
}

#[tokio::test]
async fn test_env_variables() {
    connect(|client| async move {
        let envs = client
            .container()
            .from("alpine:3.20.2")
            .with_env_variable("FOO", "bar")
            .env_variables()
            .await?;

        let names = futures::future::try_join_all(envs.iter().map(|env| env.name())).await?;

        assert_eq!(names, vec!["PATH".to_string(), "FOO".to_string()]);
        Ok(())
    })
    .await
    .unwrap();
}
