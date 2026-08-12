//! Validated scalar vocabulary shared by conformance and sign-off artifacts.
//!
//! Durable identities are portable semantic coordinates, never commands, filesystem paths,
//! provider labels, or mutable references. Resource and timing scalars reject zero and impose
//! generous format bounds so hostile input cannot create unbounded work or evidence.

use std::fmt;
use std::num::{NonZeroU32, NonZeroU64};
use std::str::FromStr;

use serde::de::Error as _;
use serde::{Deserialize, Deserializer, Serialize, Serializer};

use crate::model::{Architecture, OperatingSystem, ValueError};

const MAX_IDENTIFIER_BYTES: usize = 192;
const MAX_DURATION_MILLIS: u64 = 24 * 60 * 60 * 1_000;
const MAX_COUNT: u32 = 1_000_000;
const MAX_RESOURCE_BYTES: u64 = 1 << 60;

fn validate_identifier(value: &str) -> Result<(), &'static str> {
    if value.is_empty() || value.len() > MAX_IDENTIFIER_BYTES {
        return Err("must be non-empty and at most 192 bytes");
    }
    if value.starts_with('/')
        || value.ends_with('/')
        || value.contains("//")
        || value
            .split('/')
            .any(|segment| matches!(segment, "." | ".."))
    {
        return Err("must contain canonical non-empty relative segments");
    }
    if !value.bytes().all(|byte| {
        byte.is_ascii_lowercase()
            || byte.is_ascii_digit()
            || matches!(byte, b'-' | b'_' | b'.' | b'/')
    }) {
        return Err("must use lowercase ASCII identifier characters");
    }
    Ok(())
}

macro_rules! conformance_id {
    ($name:ident, $doc:literal) => {
        #[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
        #[doc = $doc]
        pub struct $name(String);

        impl $name {
            /// Validates and constructs the portable identity.
            pub fn new(value: impl Into<String>) -> Result<Self, ValueError> {
                let value = value.into();
                validate_identifier(&value).map_err(|reason| {
                    ValueError::from_str_for_conformance(stringify!($name), reason)
                })?;
                Ok(Self(value))
            }

            /// Borrows the canonical identity spelling.
            pub fn as_str(&self) -> &str {
                &self.0
            }
        }

        impl fmt::Display for $name {
            fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str(self.as_str())
            }
        }

        impl FromStr for $name {
            type Err = ValueError;

            fn from_str(value: &str) -> Result<Self, Self::Err> {
                Self::new(value)
            }
        }

        impl Serialize for $name {
            fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
            where
                S: Serializer,
            {
                serializer.serialize_str(self.as_str())
            }
        }

        impl<'de> Deserialize<'de> for $name {
            fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
            where
                D: Deserializer<'de>,
            {
                Self::new(String::deserialize(deserializer)?).map_err(D::Error::custom)
            }
        }
    };
}

conformance_id!(
    AssertionId,
    "Stable identity of one observable conformance assertion."
);
conformance_id!(
    SignoffCaseId,
    "Stable identity of one closed sign-off case."
);
conformance_id!(
    FixtureContextId,
    "Stable semantic context in which an assertion executes."
);
conformance_id!(
    ReviewedFixtureId,
    "Reviewed fixture registered by the closed executor."
);
conformance_id!(
    ProvenanceId,
    "Immutable reviewed provenance identity for an external input."
);
conformance_id!(
    FindingId,
    "Stable vulnerability or policy finding identity."
);
conformance_id!(
    NetworkPolicyId,
    "Closed network policy selected by a case or host profile."
);

/// Closed durable format used by conformance artifacts introduced for SDK sign-off.
#[derive(
    Clone, Copy, Debug, Default, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize,
)]
pub enum ConformanceFormatVersion {
    #[serde(rename = "1.0.0")]
    #[default]
    V1,
}

/// Canonical OS and architecture pair used by host and artifact policy.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PlatformDescriptor {
    pub operating_system: OperatingSystem,
    pub architecture: Architecture,
}

impl PlatformDescriptor {
    /// Returns the exact first sign-off platform.
    pub const fn linux_amd64() -> Self {
        Self {
            operating_system: OperatingSystem::Linux,
            architecture: Architecture::Amd64,
        }
    }
}

/// Semantic role of a pinned toolchain; ambient host tools never acquire one implicitly.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ToolchainRole {
    PreflightHost,
    ArtifactBuilder,
    NativeVerification,
    ArtifactScanner,
}

macro_rules! bounded_non_zero {
    ($name:ident, $inner:ty, $max:expr, $doc:literal) => {
        #[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize)]
        #[serde(transparent)]
        #[doc = $doc]
        pub struct $name($inner);

        impl $name {
            /// Constructs the scalar when it is non-zero and within the durable format bound.
            pub fn new(value: $inner) -> Result<Self, &'static str> {
                if value == 0 || value > $max {
                    return Err("must be non-zero and within the format bound");
                }
                Ok(Self(value))
            }

            /// Returns the validated numeric value.
            pub const fn get(self) -> $inner {
                self.0
            }
        }

        impl<'de> Deserialize<'de> for $name {
            fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
            where
                D: Deserializer<'de>,
            {
                Self::new(<$inner>::deserialize(deserializer)?).map_err(D::Error::custom)
            }
        }
    };
}

bounded_non_zero!(
    NonZeroMillis,
    u64,
    MAX_DURATION_MILLIS,
    "Positive bounded duration serialized as milliseconds."
);
bounded_non_zero!(
    NonZeroCount,
    u32,
    MAX_COUNT,
    "Positive bounded execution or lifecycle count."
);
bounded_non_zero!(
    NonZeroBytes,
    u64,
    MAX_RESOURCE_BYTES,
    "Positive bounded resource capacity in bytes."
);

impl From<NonZeroU32> for NonZeroCount {
    fn from(value: NonZeroU32) -> Self {
        Self(value.get())
    }
}

impl TryFrom<NonZeroU64> for NonZeroMillis {
    type Error = &'static str;

    fn try_from(value: NonZeroU64) -> Result<Self, Self::Error> {
        Self::new(value.get())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn identifiers_reject_paths_controls_and_mutable_reference_syntax() {
        for value in [
            "",
            "/case",
            "case/../other",
            "Case",
            "case\nother",
            "image:latest",
        ] {
            assert!(SignoffCaseId::new(value).is_err(), "accepted {value:?}");
        }
    }

    #[test]
    fn bounded_scalars_reject_zero_and_excessive_values() {
        assert!(NonZeroMillis::new(0).is_err());
        assert!(NonZeroMillis::new(MAX_DURATION_MILLIS + 1).is_err());
        assert!(NonZeroCount::new(0).is_err());
        assert!(NonZeroBytes::new(0).is_err());
    }
}
