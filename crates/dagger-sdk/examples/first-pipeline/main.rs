fn main() -> eyre::Result<()> {
    let client = dagger_sdk::connect()?;

    let version = client
        .container(None)
        .from("golang:1.19")
        .with_exec(vec!["go", "version".into()], None)
        .stdout()?;

    println!("Hello from Dagger and {}", version.trim());

    Ok(())
}
