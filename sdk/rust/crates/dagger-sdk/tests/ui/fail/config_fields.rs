fn main() {
    let config = dagger_sdk::ClientConfig::default();
    let _ = config.config_path;
    let _ = config.timeout_ms;
    let _ = config.execute_timeout_ms;
}
