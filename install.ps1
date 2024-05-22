#Requires -Version 7.0

param (
    [Parameter(Mandatory = $false)] [System.Management.Automation.SemanticVersion]$DaggerVersion,
    [Parameter(Mandatory = $false)] [string]$DaggerCommit,
    [Parameter(Mandatory = $false)] [string]$InstallPath = $env:USERPROFILE + '\dagger',

    [Parameter(Mandatory = $false)] [System.Boolean]$InteractiveInstall = $false
)

# ---------------------------------------------------------------------------------
# Author: Alessandro Festa
# Co Author: Brittan DeYoung
# Dagger Installation Utility for Windows users
# ---------------------------------------------------------------------------------

$name="dagger"
$base="https://dl.dagger.io"

function execute {
    $url = base_url
    $filename = tarball
    $url = $url + "/" + $filename
    write-host $url
    if ($InteractiveInstall) {
        Pause
    }

    Invoke-WebRequest -Uri $url -OutFile $env:temp/$filename -ErrorAction Stop
    Expand-Archive -Path $env:temp/$filename -DestinationPath $InstallPath -Force -ErrorVariable ProcessError;
    If ($ProcessError) {
@"
---------------------------------------------------------------------------
Whoops apparently we had an issue in unzipping the file, please check
you have the right permission to do so and try to unzip manually the file.
Currently we saved Dagger at your temp folder.
---------------------------------------------------------------------------
"@
        exit
    } else {
@"

Thank You for downloading Dagger!

-----------------------------------------------------
Dagger has been saved at <YOUR HOME FOLDER>/dagger/
Please add dagger.exe to your PATH in order to use it
----------------------------------------------------

"@
    }

}

function latest_version {
    $response = Invoke-RestMethod 'http://dl.dagger.io/dagger/latest_version' -Method 'GET' -Body $body -ErrorAction SilentlyContinue -ErrorVariable DownloadError
    If ($DownloadError) {
@"
---------------------------------------------------------------------------
Houston we have a problem!

Apparently we had an issue in downloading the file, please try again
run the script and if it still fail please open an issue on the Dagger repo.
----------------------------------------------------------------------------
"@
        exit
    }
    $response=$response -replace '[""]'
    $response=$response -replace '\n'
    return $response
}

function base_url {
    if ($DaggerVersion) {
        $path = "releases/" + $DaggerVersion
    } elseif ($DaggerCommit) {
        $path = "main/" + $DaggerCommit
    } else {
        $path = "releases/" + (latest_version)
    }
    $url = $base + "/" + $name + "/" + $path
    return $url
}

function tarball {
    if ($DaggerVersion) {
        $version = "v" + $DaggerVersion
    } elseif ($DaggerCommit) {
        $version = $DaggerCommit
    } else {
        $version = "v" + (latest_version)
    }
    $fileName="dagger_" + $version + "_windows_amd64"
    $filename = $filename + ".zip"
    return $filename
}

execute
