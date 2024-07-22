#[derive(clap::Parser)]
#[command(version, about)]
pub struct Configuration {
    #[arg(env, short, long, default_value = "3000")]
    pub port: u16,
}
