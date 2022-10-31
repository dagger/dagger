Push-Location "$PSScriptRoot\build" -ErrorAction Stop
& go run . $args
Pop-Location
exit $global:LASTEXITCODE
