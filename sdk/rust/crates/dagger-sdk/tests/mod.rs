mod issues;

use dagger_sdk::connect;
use pretty_assertions::assert_eq;

#[tokio::test]
async fn test_example_container() {
    let client = connect().await.unwrap();

    let alpine = client.container().from("alpine:3.16.2");

    let out = alpine
        .with_exec(vec!["cat", "/etc/alpine-release"])
        .stdout()
        .await
        .unwrap();

    assert_eq!(out, "3.16.2\n".to_string())
}

#[tokio::test]
async fn test_directory() {
    let c = connect().await.unwrap();

    let contents = c
        .directory()
        .with_new_file("/hello.txt", "world")
        .file("/hello.txt")
        .contents()
        .await
        .unwrap();

    assert_eq!("world", contents)
}

#[tokio::test]
async fn test_git() {
    let c = connect().await.unwrap();

    let tree = c.git("github.com/dagger/dagger").branch("main").tree();

    let _ = tree
        .entries()
        .await
        .unwrap()
        .iter()
        .find(|f| f.as_str() == "README.md")
        .unwrap();

    let readme_file = tree.file("README.md");

    let readme = readme_file.contents().await.unwrap();
    assert_eq!(true, readme.find("Dagger").is_some());

    let other_readme = c.file(readme_file).contents().await.unwrap();

    assert_eq!(readme, other_readme);
}

#[tokio::test]
async fn test_container() {
    let client = connect().await.unwrap();

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
        .container_opts(dagger_sdk::QueryContainerOpts {
            id: Some(id),
            platform: None,
        })
        .file("/etc/alpine-release")
        .contents()
        .await
        .unwrap();
    assert_eq!(contents, "3.16.2\n".to_string());
}
