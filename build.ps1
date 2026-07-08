# Baut CodePortable.exe als abhängigkeitsfreie Standalone-EXE.
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

# Icon (code.ico) + Manifest als Windows-Ressource (.syso) einbetten -
# wird von "go build" automatisch mitgelinkt.
Write-Host "Erzeuge Windows-Ressourcen ..."
go run github.com/akavel/rsrc@v0.10.2 -ico code.ico -manifest app.manifest -arch amd64 -o rsrc_windows_amd64.syso

# Statisch bauen: kein CGO, GUI-Subsystem (kein Konsolenfenster), Symbole entfernt
Write-Host "Baue CodePortable.exe ..."
$env:CGO_ENABLED = '0'
go build -trimpath -ldflags '-s -w -H=windowsgui' -o CodePortable.exe .

Write-Host ("Fertig: {0:N1} MB" -f ((Get-Item CodePortable.exe).Length / 1MB))
