# publish.ps1 - Tag the current commit and push it, which triggers the GitHub
# Actions release build (it builds CodePortable.exe and creates the GitHub
# Release). You commit your changes yourself first; this script never commits,
# it only tags and pushes.
#
#   .\publish.ps1          # prompts with the last tag prefilled for editing
#   .\publish.ps1 1.2.0    # use an explicit version, no prompt
param([string]$Version)

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

function Fail($m) { Write-Host "ERROR: $m" -ForegroundColor Red; exit 1 }

# Must be a git repository.
git rev-parse --is-inside-work-tree *> $null
if ($LASTEXITCODE -ne 0) { Fail "not a git repository" }
$branch = (git rev-parse --abbrev-ref HEAD).Trim()

# Refuse to tag with uncommitted changes - the tag would point at the last
# commit and silently exclude your work.
if (@(git status --porcelain).Count -gt 0) {
    Fail "you have uncommitted changes - commit them first, then run publish again."
}

# Never publish a broken build.
Write-Host "Running go vet + tests ..." -ForegroundColor Cyan
go vet ./...
if ($LASTEXITCODE -ne 0) { Fail "go vet failed - fix it before releasing" }
go test ./...
if ($LASTEXITCODE -ne 0) { Fail "tests failed - fix them before releasing" }

# Latest existing SemVer tag (best-effort fetch from origin first).
git fetch --tags --quiet
$tags = @(git tag | Where-Object { $_ -match '^[0-9]+\.[0-9]+\.[0-9]+$' })
$latest = $tags | Sort-Object { [version]$_ } | Select-Object -Last 1

# Determine the new version.
if ($Version) {
    $newVer = $Version.Trim()
} elseif ($latest) {
    # Prefill the prompt with the last tag so you can edit it and press Enter.
    Write-Host ""
    Write-Host -NoNewline "Release version (last was $latest - edit and press Enter): "
    try {
        Add-Type -AssemblyName System.Windows.Forms
        [System.Windows.Forms.SendKeys]::SendWait($latest)
    } catch { }
    $newVer = (Read-Host).Trim()
    if ([string]::IsNullOrWhiteSpace($newVer)) { $newVer = $latest }
} else {
    $newVer = (Read-Host "Release version (e.g. 0.1.0)").Trim()
}

# Validate.
if ($newVer -notmatch '^[0-9]+\.[0-9]+\.[0-9]+$') { Fail "'$newVer' is not a valid version (expected x.y.z)" }
if ($tags -contains $newVer) { Fail "tag '$newVer' already exists - choose a higher version" }

Write-Host ""
Write-Host "Release $newVer from branch '$branch' (last tag: $(if ($latest) { $latest } else { 'none' }))" -ForegroundColor Yellow
$ok = Read-Host "Push branch + tag and start the release? [y/N]"
if ($ok -notmatch '^[yY]') { Write-Host "Aborted." ; exit 0 }

# Push the branch first so the tagged commit exists on GitHub, then the tag.
Write-Host "Pushing $branch ..." -ForegroundColor Cyan
git push origin $branch
if ($LASTEXITCODE -ne 0) { Fail "pushing '$branch' failed" }

Write-Host "Tagging and pushing $newVer ..." -ForegroundColor Cyan
git tag $newVer
if ($LASTEXITCODE -ne 0) { Fail "creating the tag failed" }
git push origin $newVer
if ($LASTEXITCODE -ne 0) {
    git tag -d $newVer *> $null
    Fail "pushing the tag failed (removed the local tag again)"
}

Write-Host ""
Write-Host "Release $newVer started." -ForegroundColor Green
Write-Host "  Build:   https://github.com/jboxberger/codeportable/actions"
Write-Host "  Release: https://github.com/jboxberger/codeportable/releases"
