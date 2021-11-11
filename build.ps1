$version=$args[0]

if ($version -eq $null) {
    $version = "unknown"
    Write-Output "Version was set to unknown"
}

Write-Output "Building batch_pero_ocr $version"
$Env:GOOS = "linux"; $Env:GOARCH = "amd64"; go build --ldflags="-w -s -X 'main.version=$version'" -o .\bin\linux\ocr
$Env:GOOS = "darwin"; $Env:GOARCH = "amd64"; go build --ldflags="-w -s -X 'main.version=$version'" -o .\bin\mac\ocr-x64
$Env:GOOS = "darwin"; $Env:GOARCH = "arm64"; go build --ldflags="-w -s -X 'main.version=$version'" -o .\bin\mac\ocr-m1
$Env:GOOS = "windows"; $Env:GOARCH = "amd64"; go build --ldflags="-w -s -X 'main.version=$version'" -o .\bin\windows\ocr.exe

Write-Output "You're ready to Go :)"
