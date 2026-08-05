//! Policy for durable, argv-only verification commands.
//!
//! [`CommandSpec`] deliberately cannot express a shell command. This
//! module adds the contextual allowlists that a portable model cannot carry by itself: approved
//! executables, repository working directories, and non-secret environment keys.

use std::collections::BTreeSet;

use crate::model::{CommandSpec, ExecutableId, RepositoryRelativePath};

#[derive(Clone, Debug, Default, Eq, PartialEq)]
/// Reviewed execution boundary for one class of verification commands.
pub struct CommandPolicy {
    /// Executable identities that an adapter may resolve to exact local bytes.
    pub programs: BTreeSet<ExecutableId>,
    /// Repository-relative directories from which commands may run.
    pub working_directories: BTreeSet<RepositoryRelativePath>,
    /// Documented non-secret variables passed after the process environment is cleared.
    pub environment_keys: BTreeSet<String>,
}

/// Returns every command-policy defect in stable lexical order.
pub fn command_defects(command: &CommandSpec, policy: &CommandPolicy) -> Vec<String> {
    let mut defects = BTreeSet::new();
    if !policy.programs.contains(&command.program) {
        defects.insert(format!("program {} is not allowlisted", command.program));
    }
    if !policy
        .working_directories
        .contains(&command.working_directory)
    {
        defects.insert(format!(
            "working directory {} is outside the reviewed command roots",
            command.working_directory
        ));
    }
    for (key, value) in &command.environment {
        if !policy.environment_keys.contains(key) || is_secret_environment_key(key) {
            defects.insert(format!(
                "environment key {key} is not an allowed non-secret input"
            ));
        }
        if value.chars().any(char::is_control) {
            defects.insert(format!(
                "environment value for {key} contains control characters"
            ));
        }
    }
    defects.into_iter().collect()
}

fn is_secret_environment_key(key: &str) -> bool {
    let key = key.to_ascii_uppercase();
    ["TOKEN", "SECRET", "PASSWORD", "CREDENTIAL", "PRIVATE_KEY"]
        .iter()
        .any(|fragment| key.contains(fragment))
}

#[cfg(test)]
mod tests {
    use std::collections::{BTreeMap, BTreeSet};

    use crate::model::{CommandSpec, ExecutableId, RepositoryRelativePath};

    use super::*;

    #[test]
    fn command_policy_rejects_secret_keys_even_if_mistakenly_allowlisted() {
        let command = CommandSpec {
            program: ExecutableId::new("cargo").unwrap(),
            args: vec!["test".to_owned()],
            working_directory: RepositoryRelativePath::new("sdk/rust").unwrap(),
            environment: BTreeMap::from([("API_TOKEN".to_owned(), "redacted".to_owned())]),
        };
        let policy = CommandPolicy {
            programs: BTreeSet::from([ExecutableId::new("cargo").unwrap()]),
            working_directories: BTreeSet::from([RepositoryRelativePath::new("sdk/rust").unwrap()]),
            environment_keys: BTreeSet::from(["API_TOKEN".to_owned()]),
        };

        assert_eq!(command_defects(&command, &policy).len(), 1);
    }
}
