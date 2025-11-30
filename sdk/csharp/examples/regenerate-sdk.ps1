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
    "multi-file-example"
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

# Process base modules first
foreach ($moduleName in $baseToProcess) {
    $modulePath = Join-Path . $moduleName
    
    if (-not (Test-Path $modulePath)) {
        Write-Host "⚠ Module '$moduleName' not found, skipping..." -ForegroundColor Yellow
        Write-Host ""
        continue
    }
    Write-Host "Processing base module: $moduleName" -ForegroundColor Cyan
    
    # Check if sdk folder exists and delete it
    $sdkPath = Join-Path $modulePath "sdk"
    if (Test-Path $sdkPath) {
        Write-Host "  Deleting sdk folder..." -ForegroundColor Yellow
        Remove-Item -Path $sdkPath -Recurse -Force
    }
    
    # Run dagger develop
    Write-Host "  Running dagger develop..." -ForegroundColor Green
    Push-Location $modulePath
    try {
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
