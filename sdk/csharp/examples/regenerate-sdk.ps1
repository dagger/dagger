#!/usr/bin/env pwsh

param(
    [Parameter(ValueFromRemainingArguments)]
    [string[]]$Modules
)

# Define module dependencies
$moduleDependencies = @{
    "consumer-example" = @(
        "constructor-example",
        "defaults-example",
        "attributes-example",
        "multi-file-example",
        "interface-example",
        "processor-impl"
    )
}

# All available modules
$allBaseModules = @(
    "interface-example",
    "processor-impl",
    "constructor-example",
    "defaults-example",
    "attributes-example",
    "multi-file-example",
    "factory-example",
    "experimental-example"
)

$allConsumerModules = @(
    "consumer-example"
)

# Determine which modules to process
if ($Modules.Count -eq 0) {
    # No arguments - process all modules
    $modulesToProcess = $allBaseModules + $allConsumerModules
    Write-Host "=== Regenerating SDKs for all modules ===" -ForegroundColor Cyan
} else {
    # Process only specified modules
    $modulesToProcess = $Modules
    Write-Host "=== Regenerating SDKs for: $($Modules -join ', ') ===" -ForegroundColor Cyan
}

Write-Host ""

# Separate into base and consumer modules
$baseToProcess = $modulesToProcess | Where-Object { $allBaseModules -contains $_ }
$consumerToProcess = $modulesToProcess | Where-Object { $allConsumerModules -contains $_ }

# Process base modules in parallel (they have no dependencies)
if ($baseToProcess.Count -gt 0) {
    Write-Host "Running $($baseToProcess.Count) base modules in parallel..." -ForegroundColor Cyan
    Write-Host ""

    $jobs = @()
    foreach ($moduleName in $baseToProcess) {
        $modulePath = Join-Path . $moduleName

        if (-not (Test-Path $modulePath)) {
            Write-Host "⚠ Module '$moduleName' not found, skipping..." -ForegroundColor Yellow
            continue
        }

        # Create a script block for parallel execution
        $scriptBlock = {
            param($ModulePath, $ModuleName)

            $result = @{
                Module = $ModuleName
                Success = $false
                Error = $null
            }

            try {
                Set-Location $ModulePath

                # Delete sdk folder if exists
                $sdkPath = Join-Path $ModulePath "sdk"
                if (Test-Path $sdkPath) {
                    Remove-Item -Path $sdkPath -Recurse -Force
                }

                # Run dagger develop
                $output = dagger develop 2>&1
                if ($LASTEXITCODE -eq 0) {
                    $result.Success = $true
                } else {
                    $result.Error = "Exit code: $LASTEXITCODE"
                }
            } catch {
                $result.Error = $_.Exception.Message
            }

            return $result
        }

        Write-Host "  Starting: $moduleName" -ForegroundColor Gray
        $job = Start-Job -ScriptBlock $scriptBlock -ArgumentList (Resolve-Path $modulePath), $moduleName
        $jobs += $job
    }

    # Wait for all jobs to complete
    if ($jobs.Count -gt 0) {
        Write-Host "  Waiting for modules to complete..." -ForegroundColor Yellow
        Wait-Job -Job $jobs | Out-Null

        # Collect results
        $failed = $false
        foreach ($job in $jobs) {
            $result = Receive-Job -Job $job
            if ($result.Success) {
                Write-Host "  [$($result.Module)] ✓ Success" -ForegroundColor Green
            } else {
                Write-Host "  [$($result.Module)] ✗ Failed: $($result.Error)" -ForegroundColor Red
                $failed = $true
            }
            Remove-Job -Job $job
        }

        if ($failed) {
            Write-Host ""
            Write-Host "⚠ Some base modules failed" -ForegroundColor Red
            exit 1
        }
    }

    Write-Host ""
}

# Process consumer modules
foreach ($moduleName in $consumerToProcess) {
    $modulePath = Join-Path . $moduleName

    if (-not (Test-Path $modulePath)) {
        Write-Host "⚠ Module '$moduleName' not found, skipping..." -ForegroundColor Yellow
        Write-Host ""
        continue
    }
    Write-Host "Processing consumer module: $moduleName" -ForegroundColor Cyan

    # Check if sdk folder exists and delete it
    $sdkPath = Join-Path $modulePath "sdk"
    if (Test-Path $sdkPath) {
        Write-Host "  Deleting sdk folder..." -ForegroundColor Yellow
        Remove-Item -Path $sdkPath -Recurse -Force
    }

    Push-Location $modulePath
    try {
        # Install dependencies
        $dependencies = $moduleDependencies[$moduleName]
        if ($dependencies) {
            Write-Host "  Installing dependencies..." -ForegroundColor Yellow
            foreach ($dep in $dependencies) {
                Write-Host "    Installing $dep..." -ForegroundColor Gray
                $depPath = Join-Path ".." $dep
                dagger install $depPath
                if ($LASTEXITCODE -ne 0) {
                    Write-Host "    ✗ Failed to install $dep" -ForegroundColor Red
                }
            }
        }

        # Run dagger develop
        Write-Host "  Running dagger develop..." -ForegroundColor Green
        dagger develop
        if ($LASTEXITCODE -eq 0) {
            Write-Host "  ✓ Success" -ForegroundColor Green
        } else {
            Write-Host "  ✗ Failed with exit code $LASTEXITCODE" -ForegroundColor Red
        }
    } finally {
        Pop-Location
    }

    Write-Host ""
}

Write-Host "All done!" -ForegroundColor Green
