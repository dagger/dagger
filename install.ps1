# param (
#     [Parameter(Mandatory)] $PersonalToken  
# )
Clear-Host
@"

---------------------------------------------------------------------------------
Author: Alessandro Festa
Dagger Installation Utility  for Windows users
---------------------------------------------------------------------------------

"@

# Since we are already authenticated we may directly download latest version.
$name="dagger"
$base="https://dagger-io.s3.amazonaws.com"
function http_download {
    $version=Get_Version
    $version=$version -replace '[""]'
    $version=$version -replace '\n'
    $fileName="dagger_v" + $version + "_windows_amd64"
    Clear-Host
    $url = $base + "/" + $name + "/releases/" + $version + "/" + $fileName + ".zip"
    write-host $url
    Pause
    

    Invoke-WebRequest -Uri $url -OutFile $env:temp/$fileName.zip -ErrorAction Stop
    Expand-Archive -Path $env:temp/$fileName.zip -DestinationPath $env:HOMEPATH/dagger -Force -ErrorVariable ProcessError;
    If ($ProcessError)
    {
    Clear-Host
@"
Whoops apparently we had an issue in unzipping the file, please check
you have the right permission to do so and try to unzip manually the file.
Currently we saved Dagger at your temp folder.
"@
exit
    } else {
        Clear-Host
    
@"

Thank You for downloading Dagger!

-----------------------------------------------------
Dagger has been saved at <YOUR HOME FOLDER>/dagger/
Please add dagger.exe to your PATH in order to use it
----------------------------------------------------

"@
    }

}

function Get_Version { 
    $response = Invoke-RestMethod 'http://releases.dagger.io/dagger/latest_version' -Method 'GET'  -Body $body -ErrorAction SilentlyContinue -ErrorVariable DownloadError
    If ($DownloadError)
    {
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
    return $response
    
}

http_download