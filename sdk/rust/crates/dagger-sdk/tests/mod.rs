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

        Ok(())
    })
    .await
    .unwrap();
}

// These tests document the expected API for node(id) loading and
// sync() roundtrips. They are commented out because they reference
// APIs that don't exist yet (Loadable trait, @expectedType codegen).
// Uncomment as each feature lands.
//
// TODO: test_node_load_container — requires Loadable trait + codegen
//   connect(|client| async move {
//       let id = client.container().from("alpine:3.16.2").id().await?;
//       let loaded: Container = client.load(id);
//       let out = loaded.with_exec(vec!["cat", "/etc/alpine-release"]).stdout().await?;
//       assert_eq!(out, "3.16.2\n");
//   })
//
// TODO: test_node_load_directory — requires Loadable trait + codegen
//   connect(|client| async move {
//       let id = client.directory().with_new_file("/hello.txt", "world").id().await?;
//       let loaded: Directory = client.load(id);
//       assert_eq!(loaded.file("/hello.txt").contents().await?, "world");
//   })
//
// TODO: test_node_load_file — requires Loadable trait + codegen
//   connect(|client| async move {
//       let id = client.directory().with_new_file("/hello.txt", "from-id")
//           .file("/hello.txt").id().await?;
//       let loaded: File = client.load(id);
//       assert_eq!(loaded.contents().await?, "from-id");
//   })
//
// TODO: test_container_sync_roundtrip — requires @expectedType codegen
//   connect(|client| async move {
//       let synced: Container = client.container().from("alpine:3.16.2")
//           .sync().await?;
//       let out = synced.with_exec(vec!["cat", "/etc/alpine-release"]).stdout().await?;
//       assert_eq!(out, "3.16.2\n");
//   })
