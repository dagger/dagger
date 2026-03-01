#!/usr/bin/env bash
set -e

# Define module dependencies
declare -A module_dependencies=(
    ["consumer-example"]="constructor-example defaults-example attributes-example multi-file-example interface-example processor-impl"
)

# All available modules
all_base_modules=(
    "interface-example"
    "processor-impl"
    "constructor-example"
    "defaults-example"
    "attributes-example"
    "multi-file-example"
    "factory-example"
    "experimental-example"
)

all_consumer_modules=(
    "consumer-example"
)

# Parse arguments
if [ $# -eq 0 ]; then
    # No arguments - process all modules
    modules_to_process=("${all_base_modules[@]}" "${all_consumer_modules[@]}")
    echo "=== Regenerating SDKs for all modules ==="
else
    # Process only specified modules
    modules_to_process=("$@")
    echo "=== Regenerating SDKs for: ${modules_to_process[*]} ==="
fi

echo ""

# Separate into base and consumer modules
base_to_process=()
consumer_to_process=()

for module in "${modules_to_process[@]}"; do
    if [[ " ${all_consumer_modules[*]} " =~ " ${module} " ]]; then
        consumer_to_process+=("$module")
    else
        base_to_process+=("$module")
    fi
done

# Process base modules in parallel (they have no dependencies)
process_base_module() {
    local module=$1
    if [ ! -d "$module" ]; then
        echo "⚠ Module '$module' not found, skipping..."
        return 1
    fi
    
    echo "Processing base module: $module"
    cd "$module" || return 1
    
    if [ -d "sdk" ]; then
        echo "  [$module] Deleting sdk folder..."
        rm -rf sdk
    fi
    
    echo "  [$module] Running dagger develop..."
    if dagger develop > "../.dagger-develop-$module.log" 2>&1; then
        echo "  [$module] ✓ Success"
        rm -f "../.dagger-develop-$module.log"
        cd ..
        return 0
    else
        echo "  [$module] ✗ Failed - check .dagger-develop-$module.log"
        cd ..
        return 1
    fi
}

export -f process_base_module

if [ ${#base_to_process[@]} -gt 0 ]; then
    echo "Running ${#base_to_process[@]} base modules in parallel..."
    echo ""
    
    # Run base modules in parallel using background jobs
    pids=()
    for module in "${base_to_process[@]}"; do
        process_base_module "$module" &
        pids+=("$!")
    done
    
    # Wait for all background jobs and check exit codes
    failed=0
    for pid in "${pids[@]}"; do
        if ! wait "$pid"; then
            failed=1
        fi
    done
    
    echo ""
    if [ $failed -ne 0 ]; then
        echo "⚠ Some base modules failed" >&2
        exit 1
    fi
fi

# Process consumer modules
for module in "${consumer_to_process[@]}"; do
    if [ ! -d "$module" ]; then
        echo "⚠ Module '$module' not found, skipping..."
        echo ""
        continue
    fi
    
    echo "Processing consumer module: $module"
    cd "$module"
    
    if [ -d "sdk" ]; then
        echo "  Deleting sdk folder..."
        rm -rf sdk
    fi
    
    # Install dependencies if defined
    deps="${module_dependencies[$module]}"
    if [ -n "$deps" ]; then
        echo "  Installing dependencies..."
        for dep in $deps; do
            echo "    Installing $dep..."
            dagger install ../$dep
        done
    fi
    
    echo "  Running dagger develop..."
    if dagger develop; then
        echo "  ✓ Success"
    else
        echo "  ✗ Failed"
        exit 1
    fi
    
    cd ..
    echo ""
done

echo "All done!"
