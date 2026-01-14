#!/usr/bin/env nu
# Main test module for Nushell SDK
# Exports all test checks for Dagger to discover

use /usr/local/lib/dag.nu *

# Re-export all test functions from test files
use core.nu *
use wrappers.nu *
use objects.nu *
