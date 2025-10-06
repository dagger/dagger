#Requires -Version 7.0

<#
.Description
    Download and install dagger.
.PARAMETER DaggerVersion
    Semver version of dagger to install.
.PARAMETER DaggerCommit
    Commit SHA for a dev build of dagger to install.
.EXAMPLE
    .\install.ps1
    Install with default settings.
.EXAMPLE
    .\install.ps1 -InstallPath path\to\dir
    Install to path/to/dir.
.EXAMPLE
    .\install.ps1 -DaggerVersion vX.Y.Z
    Install specified version vX.Y.Z.
.EXAMPLE
    .\install.ps1 -DaggerCommit head
    Install latest dev build.
.EXAMPLE
    .\install.ps1 -DaggerCommit [commit]
    Install specified dev build [commit].
#>

Param (
    [Parameter(Mandatory = $false)][string]$DaggerVersion,
    [Parameter(Mandatory = $false)][string][ValidatePattern("^(?:head|(?:[0-9a-fA-F]{40}))?$")]$DaggerCommit,
    [Parameter(Mandatory = $false)][string]$DownloadPath = [System.IO.Path]::GetTempFileName(),
    [Parameter(Mandatory = $false)][string]$InstallPath = "$env:USERPROFILE\dagger",
    [Parameter(Mandatory = $false)][switch]$AddToPath = $false,
    [Parameter(Mandatory = $false)][switch]$Interactive = $false
)

# ---------------------------------------------------------------------------------
# Author: Alessandro Festa
# Co Author: Brittan DeYoung
# Dagger Installation Utility for the windows dagger.exe binary
# ---------------------------------------------------------------------------------

# This function prompts the user for a download location and validates it.
function Get-DownloadPath {
    $defaultPath = $DownloadPath
    while ($true) {
        $inputPath = Read-Host -Prompt "Enter the download location or leave empty and hit Enter for default ($defaultPath)"
        if ([string]::IsNullOrWhiteSpace($inputPath)) {
            return $defaultPath
        }
        elseif (Test-Path $inputPath -IsValid) {
            return $inputPath
        }
        else {
            Write-Output "Invalid path: $inputPath. Please enter a valid path."
        }
    }
}

function Get-ProcessorArchitecture {
    $arch = $env:PROCESSOR_ARCHITECTURE
    switch ($arch) {
        "AMD64" {
            return "amd64"
        }
        "ARM64" {
            return "arm64"
        }
        "ARM" {
            return "armv7"
        }
        default {
            throw "Unsupported architecture: $arch"
        }
    }
}

function Get-ValidPath {
    Param (
        [Parameter(Mandatory = $true)]
        [string]$Message,
        [Parameter(Mandatory = $true)]
        [string]$Default
    )

    $path = $null
    while ($null -eq $path) {
        $path = Read-Host -Prompt $Message

        if ([string]::IsNullOrWhiteSpace($path)) {
            return $Default
        }
        else {
            if ((Test-Path $path -IsValid) -and (Test-Path $path -IsValid -PathType Leaf)) {
                return $path
            }
            else {
                Write-Warning "Invalid path: $path. Please enter a valid path."
                $path = $null
            }
        }
    }
}

function Confirm-GitCommit {
    # Check if the hash matches the basic pattern of a Git commit hash
    if (-not ([string]::IsNullOrWhiteSpace($DaggerCommit)) -and $DaggerCommit -match '^[0-9a-fA-F]{40}$') {
        Write-Output @"
---------------------------------------------------------------------------
The commit hash provided does not seem to be a valid Git commit hash.
Please provide a valid Git commit hash.
---------------------------------------------------------------------------
"@
        exit 1
    }
}

function Get-DownloadUrl {
    $fileName = Get-FileName

    if (-not [string]::IsNullOrWhiteSpace($DaggerCommit)) {
        return "https://dl.dagger.io/dagger/main/${DaggerCommit}/${fileName}"
    }

    return "https://dl.dagger.io/dagger/releases/${DaggerVersion}/${fileName}"
}

function Get-ChecksumUrl {
    if (-not [string]::IsNullOrWhiteSpace($DaggerCommit)) {
        return "https://dl.dagger.io/dagger/main/${DaggerCommit}/checksums.txt"
    }

    return "https://dl.dagger.io/dagger/releases/${DaggerVersion}/checksums.txt"
}

# Used for interactive mode to get a true or false response from the user
function Get-TrueFalse {
    Param (
        [Parameter(Mandatory = $true)][string]$Message,
        [Parameter(Mandatory = $true)][bool]$Default
    )

    $response = ""
    while ($response -notmatch "^(y|n)$") {
        $response = Read-Host -Prompt $Message
        if ([string]::IsNullOrWhiteSpace($response)) {
            $response = $Default ? "y" : "n"
            break
        }
    }

    return $response.StartsWith("y")
}

function Find-LatestVersion {
    $body = $null
    $response = Invoke-RestMethod "https://dl.dagger.io/dagger/latest_version" -Body $body -ErrorVariable LatestVersionError

    if ($LatestVersionError) {
        Write-Error @"
---------------------------------------------------------------------------
Houston we have a problem!
Apparently we had an issue finding the latest version of Dagger.
Please check https://docs.dagger.io/install
----------------------------------------------------------------------------
"@
        exit 1
    }

    $latestVersion = $response -replace "v", ""
    return [System.Management.Automation.SemanticVersion]::Parse($latestVersion)
}

function Find-Version {
    $body = $null
    try {
        $response = Invoke-RestMethod "https://dl.dagger.io/dagger/versions/$DaggerVersion" -Body $body
    } catch {
        return $DaggerVersion
    }
    $version = $response -replace "v", ""
    return [System.Management.Automation.SemanticVersion]::Parse($version)
}

# This function returns the file name of the Dagger zip file to download.
function Get-FileName {
    $arch = Get-ProcessorArchitecture

    if (-not [string]::IsNullOrWhiteSpace($DaggerCommit)) {
        return "dagger_${DaggerCommit}_windows_${arch}.zip"
    }

    return "dagger_v${DaggerVersion}_windows_${arch}.zip"
}

# This function prompts the user for a version string and validates it.

$semVerPattern = "^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$"

function Get-SemVer {
    Param (
        [Parameter(Mandatory = $true)]
        [string]$Message,
        [Parameter(Mandatory = $true)]
        [System.Management.Automation.SemanticVersion]$Default
    )

    $version = $null

    while ($null -eq $version) {
        $inputString = Read-Host -Prompt $Message

        if ([string]::IsNullOrWhiteSpace($inputString)) {
            return $Default
        }

        $isValid = [System.Management.Automation.SemanticVersion]::TryParse($inputString, [ref]$version) -and $inputString -match $semVerPattern

        if (-not $isValid) {
            $version = $null
            Write-Warning "Invalid version string: $inputString. Please enter a valid semantic version (e.g., 0.11.6)."
        }
        elseif ($isvalid -and $version -gt $Default) {
            $version = $null
            Write-Warning "Please enter a valid version."
        }
    }

    return $version
}

# This gets a full path name from the install path i.e C:\users\username\dagger
function Get-InstallPath {
    if (-not (Test-Path $InstallPath)) {
        New-Item -ItemType Directory -Path $InstallPath -ErrorAction Stop -ErrorVariable InstallPathError | Out-Null

        if ($InstallPathError) {
            Write-Error @"
---------------------------------------------------------------------------
Whoops, apparently we had an issue in creating the install path.
Please check you have the right permission to do so or try to create the path manually.
---------------------------------------------------------------------------
"@
            exit 1
        }
    }

    return (Get-Item -Path $InstallPath).FullName
}

# This function extracts the checksum of the downloaded file which contains all checksums for a version
function Get-Checksum {
    $checksumUrl = Get-ChecksumUrl
    $arch = Get-ProcessorArchitecture
    $response = Invoke-RestMethod -Uri $checksumUrl -UserAgent "PowerShell"
    $checksums = $response -split "`n"

    $checksum = $null
    $target = $null

    if (-not [string]::IsNullOrWhiteSpace($DaggerCommit)) {
        $target = "dagger_${DaggerCommit}_windows_${arch}.zip"
    }
    else {
        $target = "dagger_v${DaggerVersion}_windows_${arch}.zip"
    }

    # Find the checksum for the target file
    foreach ($line in $checksums) {
        if ($line -match $target) {
            $checksum = $line -split " " | Select-Object -First 1
            break
        }
    }

    return $checksum
}

function Compare-Checksum {
    Param (
        [Parameter(Mandatory = $true)]
        [string]$DownloadPath,
        [Parameter(Mandatory = $true)]
        [string]$Checksum
    )

    $hash = Get-FileHash -Path $DownloadPath -Algorithm SHA256

    if ($hash.Hash -ne $Checksum) {
        Remove-Item -Path $DownloadPath

        Write-Error @"
---------------------------------------------------------------------------
The file checksum does not match the expected checksum!
Expected: $Checksum
File    : $($hash.Hash)
The downloaded file has been removed.
---------------------------------------------------------------------------
"@
        exit 1
    }
}


function Main {
    # Powershell is cross-platform, notice about windows binary when used on non-windows
    if (-not $IsWindows) {
        Write-Warning @"
---------------------------------------------------------------------------
Note: This script will install the Windows binary of Dagger.
---------------------------------------------------------------------------
"@
    }

    # Dagger compiles for AMD64, ARM64, and ARM architectures only
    Get-ProcessorArchitecture -ErrorAction Stop -ErrorVariable ArchitectureError | Out-Null

    if ($ArchitectureError) {
        Write-Error @"
---------------------------------------------------------------------------
Whoops, apparently we had an issue in determining the architecture of your system.
Dagger compiles for AMD64, ARM64, and ARM architectures only.
---------------------------------------------------------------------------
"@
        exit 1
    }

    # If the user does not provide a version, we will find the latest version
    if ([string]::IsNullOrWhiteSpace($DaggerVersion)) {
        $DaggerVersion = Find-LatestVersion
    } else {
        $DaggerVersion = Find-Version
    }

    # Interactive allows customisation of the installation
    if ($Interactive) {
        $DaggerVersion = Get-SemVer `
            -Message "Enter the Dagger version to install or leave empty and hit Enter for default ($DaggerVersion)" `
            -Default $DaggerVersion `
            -ErrorVariable InteractiveDaggerVersionError

        if ($InteractiveDaggerVersionError) {
            Write-Error @"
---------------------------------------------------------------------------
Whoops, we had an issue in finding the selected version of Dagger.
Please check your internet connection and try again.
---------------------------------------------------------------------------
"@
            exit 1
        }

        $DownloadPath = Get-ValidPath `
            -Message "Enter the download location or leave empty and hit Enter for default ($DownloadPath)" `
            -Default $DownloadPath `
            -ErrorVariable InteractiveDownloadPathError

        if ($InteractiveDownloadPathError) {
            Write-Error @"
---------------------------------------------------------------------------
Whoops, we had an issue in getting the download path.
Please check the path and try again.
---------------------------------------------------------------------------
"@
            exit 1
        }

        $InstallPath = Get-ValidPath `
            -Message "Enter the destination unzip path or leave empty and hit Enter for default ($(Get-InstallPath))" `
            -Default $InstallPath `
            -ErrorVariable InteractiveInstallPathError

        if ($InteractiveInstallPathError) {
            Write-Error @"
---------------------------------------------------------------------------
Whoops, we had an issue in getting the install path.
Please check the path and try again.
---------------------------------------------------------------------------
"@
            exit 1
        }

        $defaultString = $AddToPath ? "y" : "n"
        $AddToPath = Get-TrueFalse `
            -Message "Enter (y/n) to add dagger.exe to your PATH or hit Enter for default ($defaultString)" `
            -Default $AddToPath `
            -ErrorVariable InteractiveAddToPathError

        if ($InteractiveAddToPathError) {
            Write-Error @"
---------------------------------------------------------------------------
Whoops, we had an issue checking if you want to add dagger.exe to your PATH.
Please check the option and try again.
---------------------------------------------------------------------------
"@
            exit 1
        }
    }

    $zipUrl = Get-DownloadUrl
    Write-Output "Downloading Dagger from $zipUrl"

    Invoke-RestMethod -Uri $zipUrl -OutFile $DownloadPath -UserAgent "PowerShell"
    $checksum = Get-Checksum -ErrorAction Stop -ErrorVariable ChecksumError
    Write-Output "Checksum is $checksum"
    Compare-Checksum -DownloadPath $DownloadPath -Checksum $checksum
    Expand-Archive -Path $downloadPath -DestinationPath $InstallPath -Force -ErrorVariable ProcessError

    if ($ProcessError) {
        Write-Error @"
---------------------------------------------------------------------------
Whoops, apparently we had an issue in unzipping the file, please check
you have the right permission to do so or try to unzip the file manually.
Dagger is currently downloaded at $DownloadPath.
---------------------------------------------------------------------------
"@
    }
    else {
        $installPath = Get-InstallPath
        Write-Output @"
---------------------------------------------------------------------------
Thank you for downloading Dagger!
Dagger has been successfully installed at $(Get-InstallPath)
"@
        $path = [Environment]::GetEnvironmentVariable("Path", "User")
        $existsInPath = $path -like "*$installPath*"

        if ($AddToPath) {
            if (-not $existsInPath) {
                [Environment]::SetEnvironmentVariable("Path", [Environment]::GetEnvironmentVariable("Path", "User") + ";$installPath", "User")
                Write-Output "Dagger has been added to your PATH"
            }
            else {
                Write-Warning "Dagger is already in your PATH"
            }
        }
        else {
            if (-not $existsInPath) {
                Write-Output "Please add dagger.exe to your PATH in order to use it"
            }
        }
        Write-Output "---------------------------------------------------------------------------"
    }
}

$isInvoked = [string]::IsNullOrWhiteSpace($MyInvocation.MyCommand.Path)

if ($isInvoked) {
    # Allow Invoke-Expression to customise the installation by producing the function Install-Dagger
    # This is because the Param of the Script file is not available unless the script is invoked
    function Install-Dagger {
        Param (
            [Parameter(Mandatory = $false)][System.Management.Automation.SemanticVersion]$DaggerVersion,
            [Parameter(Mandatory = $false)][string][ValidatePattern("^(?:head|(?:[0-9a-fA-F]{40}))?$")]$DaggerCommit,
            [Parameter(Mandatory = $false)][string]$DownloadPath = [System.IO.Path]::GetTempFileName(),
            [Parameter(Mandatory = $false)][string]$InstallPath = "$env:USERPROFILE\dagger",
            [Parameter(Mandatory = $false)][switch]$AddToPath = $false,
            [Parameter(Mandatory = $false)][switch]$Interactive = $false
        )
        Main
    }
}
else {
    Main
}
