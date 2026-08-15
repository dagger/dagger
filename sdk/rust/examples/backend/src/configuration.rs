use clap::{Parser, Subcommand};

#[derive(Parser)]
#[command(version, about)]
pub(super) struct Configuration {
    #[arg(env, short, long, default_value = "3000")]
    pub(super) port: u16,
    #[command(subcommand)]
    action: Option<Action>,
}

#[derive(Subcommand)]
enum Action {
    /// Build and evaluate the service image without publishing it.
    Build,
    /// Publish the service image to an explicitly selected address.
    Publish {
        /// Complete registry address to publish.
        #[arg(long)]
        address: String,
        /// Confirms the external registry write.
        #[arg(long)]
        allow_publish: bool,
    },
}

pub(super) enum Output {
    BuildOnly,
    Publish(String),
}

impl Configuration {
    pub(super) fn into_output(self) -> eyre::Result<Output> {
        match self.action {
            None | Some(Action::Build) => Ok(Output::BuildOnly),
            Some(Action::Publish {
                address,
                allow_publish: true,
            }) => Ok(Output::Publish(address)),
            Some(Action::Publish { .. }) => {
                eyre::bail!("publishing requires the explicit --allow-publish confirmation")
            }
        }
    }
}
