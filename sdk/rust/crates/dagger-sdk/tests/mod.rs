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

// Test that a Container can be loaded from its ID via node() + inline fragment.
//
// The generated API should provide typed load methods on Query that use
// `select("node").arg("id", id).inline_fragment("Container")` under the
// hood, so the returned Container is fully usable.
#[tokio::test]
async fn test_node_load_container() {
    connect(|client| async move {
        // Get a container and its ID
        let ctr = client.container().from("alpine:3.16.2");
        let id = ctr.id().await.unwrap();

        // Load the container back via node(id) — this should use
        // inline_fragment("Container") so the returned object has
        // all Container fields available.
        let loaded: dagger_sdk::Container = client.load(id);
        let out = loaded
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

// Test that a Directory can be loaded from its ID via node().
#[tokio::test]
async fn test_node_load_directory() {
    connect(|client| async move {
        let dir = client
            .directory()
            .with_new_file("/hello.txt", "world");
        let id = dir.id().await.unwrap();

        let loaded: dagger_sdk::Directory = client.load(id);
        let contents = loaded.file("/hello.txt").contents().await.unwrap();

        assert_eq!("world", contents);

        Ok(())
    })
    .await
    .unwrap();
}

// Test that a File can be loaded from its ID via node().
#[tokio::test]
async fn test_node_load_file() {
    connect(|client| async move {
        let file = client
            .directory()
            .with_new_file("/hello.txt", "from-id")
            .file("/hello.txt");
        let id = file.id().await.unwrap();

        let loaded: dagger_sdk::File = client.load(id);
        let contents = loaded.contents().await.unwrap();

        assert_eq!("from-id", contents);

        Ok(())
    })
    .await
    .unwrap();
}

// Test round-tripping: sync() should return the parent object type,
// not a raw Id. The @expectedType directive on sync's return tells
// codegen that sync() on Container returns Container.
#[tokio::test]
async fn test_container_sync_roundtrip() {
    connect(|client| async move {
        let ctr = client.container().from("alpine:3.16.2");

        // sync() forces evaluation and returns a Container, not an Id
        let synced: dagger_sdk::Container = ctr.sync().await.unwrap();

        let out = synced
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
