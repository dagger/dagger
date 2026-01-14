#!/usr/bin/env nu
# Dagger API module
#
# This module re-exports all Dagger API operations organized by namespace.

# Export core helpers
export use core.nu dagger-query
export use core.nu load-container-from-id
export use core.nu load-directory-from-id
export use core.nu load-file-from-id

# Export all operation namespaces
export use container.nu *
export use directory.nu *
export use file.nu *
export use host.nu *
export use git.nu *
export use cache.nu *
export use secret.nu *
export use check.nu *
export use module.nu *

# Export smart wrappers for clean pipeline syntax
export use wrappers.nu *
