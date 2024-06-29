use dagger_sdk::connect;
use pretty_assertions::assert_eq;

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
