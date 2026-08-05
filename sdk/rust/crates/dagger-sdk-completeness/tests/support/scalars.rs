//! Validated scalar generators shared by all durable-model strategies.
//!
//! Generated text is intentionally small and shrink-friendly. Every value still enters through its
//! production constructor, so a model strategy cannot bypass the same canonical spelling rules
//! imposed on decoded contract artifacts.

use dagger_sdk_completeness::{
    CommitSha, DaggerVersion, Digest, NonEmptyText, RepositoryRelativePath, SemverVersion,
    SourceLocator,
};
use proptest::prelude::*;

pub fn commit_strategy() -> BoxedStrategy<CommitSha> {
    "[0-9a-f]{40}"
        .prop_map(|value| CommitSha::new(value).unwrap())
        .boxed()
}

pub fn semver_strategy() -> BoxedStrategy<SemverVersion> {
    (0_u8..10, 0_u8..20, 0_u8..30)
        .prop_map(|(major, minor, patch)| {
            SemverVersion::new(format!("{major}.{minor}.{patch}")).unwrap()
        })
        .boxed()
}

pub fn dagger_version_strategy() -> BoxedStrategy<DaggerVersion> {
    semver_strategy()
        .prop_map(|version| DaggerVersion::new(version.to_string()).unwrap())
        .boxed()
}

pub fn digest_strategy() -> BoxedStrategy<Digest> {
    any::<[u8; 32]>().prop_map(Digest::sha256).boxed()
}

pub fn text_strategy() -> BoxedStrategy<NonEmptyText> {
    "[a-z][a-z0-9-]{0,23}"
        .prop_map(|value| NonEmptyText::new(value).unwrap())
        .boxed()
}

pub fn relative_path_strategy() -> BoxedStrategy<RepositoryRelativePath> {
    ("[a-z][a-z0-9-]{0,11}", "[a-z][a-z0-9-]{0,11}")
        .prop_map(|(directory, file)| {
            RepositoryRelativePath::new(format!("{directory}/{file}.rs")).unwrap()
        })
        .boxed()
}

pub fn locator_strategy() -> BoxedStrategy<SourceLocator> {
    ("[a-z][a-z0-9_]{0,15}", 1_u16..4096)
        .prop_map(|(symbol, line)| SourceLocator::new(format!("{symbol}:{line}")).unwrap())
        .boxed()
}

pub fn alternate_commit(commit: &CommitSha) -> CommitSha {
    let replacement = if commit.as_str().starts_with('a') {
        'b'
    } else {
        'a'
    };
    CommitSha::new(format!("{replacement}{}", &commit.as_str()[1..])).unwrap()
}
