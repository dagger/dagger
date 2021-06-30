param (
    [Parameter(Mandatory)] $PersonalToken  
)
Clear-Host
@"

---------------------------------------------------------------------------------
Author: Alessandro Festa
Usage: To run using your GH personal developer token simply use the flag as below
./install.ps1 -PersonalToken 1234567891213
Dagger executable will be save under the folder "dagger" in your home folder.
---------------------------------------------------------------------------------

"@

# Since we are already authenticated we may directly download latest version.
$name="dagger"
$base="https://dagger-io.s3.amazonaws.com"
function http_download {
    Clear-Host
    $version=Get_Version
    $version= $version -replace '[""]'
    $url = $base + "/" + $name + "/releases/" + $version.substring(1) + "/dagger_" + $version + "_windows_amd64.zip"
    $fileName="dagger_" + $version + "_windows_amd64"
    

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

    $headers = New-Object "System.Collections.Generic.Dictionary[[String],[String]]"
    $headers.Add("Authorization", "token $PersonalToken")
    $headers.Add("Accept", "application/vnd.github.VERSION.raw")
    
    $response = Invoke-RestMethod 'https://api.github.com/repos/dagger/dagger/releases/latest' -Method 'GET' -Headers $headers -Body $body -ErrorAction SilentlyContinue -ErrorVariable DownloadError
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
    
    return $response.tag_name| ConvertTo-Json
    
}

http_download