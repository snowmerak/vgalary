param(
    [Parameter(Mandatory = $true)][string]$ImageDirectory,
    [Parameter(Mandatory = $true)][string]$VideoPath,
    [string]$OutputDirectory = 'testdata/local/media'
)
$ErrorActionPreference = 'Stop'
$fixtureArgs = @('run', './cmd/mkfixtures', '-out', $OutputDirectory, '-video', $VideoPath)
foreach ($name in @('camphoto_1804928587 (1).jpg', 'camphoto_959030623.jpg', 'camphoto_1903590565.jpg', 'camphoto_1804928587.jpg')) {
    $fixtureArgs += @('-image', (Join-Path $ImageDirectory $name))
}
& go @fixtureArgs
if ($LASTEXITCODE -ne 0) { throw 'Fixture generation failed' }
