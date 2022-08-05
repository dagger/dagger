#Requires -Version 7.0

param (
    # [Parameter(Mandatory)] $PersonalToken  
    [Parameter(Mandatory = $false)] [System.Management.Automation.SemanticVersion]$DaggerVersion,
    [Parameter(Mandatory = $false)] [System.Boolean]$InteractiveInstall = $false,
    [Parameter(Mandatory = $false)] [string]$InstallPath = $env:HOMEPATH + '\dagger'
)
Clear-Host
@"

---------------------------------------------------------------------------------
Author: Alessandro Festa
Co Author: Brittan DeYoung
Dagger Installation Utility  for Windows users
---------------------------------------------------------------------------------

"@

# Since we are already authenticated we may directly download latest version.
$name = "dagger"
$base = "https://dagger-io.s3.amazonaws.com"
function http_download {
    $version = Get_Version
    $version = $version -replace '[""]'
    $version = $version -replace '\n'
    $fileName = "dagger_v" + $version + "_windows_amd64"
    Clear-Host
    $url = $base + "/" + $name + "/releases/" + $version + "/" + $fileName + ".zip"
    write-host $url
    if ($InteractiveInstall) {
        Pause
    }
    

    Invoke-WebRequest -Uri $url -OutFile $env:temp/$fileName.zip -ErrorAction Stop
    Expand-Archive -Path $env:temp/$fileName.zip -DestinationPath $InstallPath -Force -ErrorVariable ProcessError;
    If ($ProcessError) {
        Clear-Host
        @"
Whoops apparently we had an issue in unzipping the file, please check
you have the right permission to do so and try to unzip manually the file.
Currently we saved Dagger at your temp folder.
"@
        exit
    }
    else {
        Clear-Host
    
        @"

Thank You for downloading Dagger!

-----------------------------------------------------
Dagger has been saved in $InstallPath
Please add dagger.exe to your PATH in order to use it
----------------------------------------------------

"@
    }

}

function Get_Version { 
    if ($DaggerVersion) {
        $version = $DaggerVersion
    }
    else {
        $response = Invoke-RestMethod 'http://releases.dagger.io/dagger/latest_version' -Method 'GET'  -Body $body -ErrorAction SilentlyContinue -ErrorVariable DownloadError
        If ($DownloadError) {
            Clear-Host
            @"

---------------------------------------------------------------------------
Houston we have a problem!

Apparently we had an issue in downloading the file, please try again
run the script and if it still fail please open an issue on the Dagger repo.
----------------------------------------------------------------------------

"@
            exit
        }
        $version = $response
    }
    return $version
    
}

http_download