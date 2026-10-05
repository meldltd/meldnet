# Run explicitly in an elevated PowerShell. The frontend never runs this script.
param([Parameter(Mandatory=$true)][string]$AllowedSID)
$ErrorActionPreference = 'Stop'
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Administrator authorization is required.' }
$sid = New-Object Security.Principal.SecurityIdentifier($AllowedSID)
if ($sid.Value -notmatch '^S-1-5-21-(\d+-){3}\d+$' -and $sid.Value -notmatch '^S-1-12-1-(\d+-){3}\d+$') { throw 'Specify an individual local, domain or Entra user SID.' }
$null = $sid.Translate([Security.Principal.NTAccount])
$target = Join-Path $env:ProgramFiles 'Meldnet'
if (Test-Path $target) {
    if ((Get-Item $target).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Installation directory must not be a reparse point.' }
}
foreach ($file in @('meldnet.exe', 'meldnetd.exe', 'wintun.dll', 'WINTUN-LICENSE.txt')) {
    $source = Join-Path $PSScriptRoot $file
    if (-not (Test-Path $source -PathType Leaf)) { throw "Missing package file: $file" }
    if ((Get-Item $source).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Package files must not be reparse points.' }
    if (Test-Path (Join-Path $target $file)) { throw 'Existing installation files found; upgrade explicitly rather than overwriting them.' }
}
if ((Get-AuthenticodeSignature (Join-Path $PSScriptRoot 'wintun.dll')).Status -ne 'Valid') { throw 'Official signed Wintun DLL required.' }
$existing = Get-Service -Name Meldnet -ErrorAction SilentlyContinue
if ($existing) { throw 'Meldnet service already exists. Preserve its authorized SID and state; stop and upgrade explicitly.' }
# Create a directory with only SYSTEM/Administrators write access before copying
# either the daemon or its DLL. Ordinary users receive read/execute access.
$acl = New-Object Security.AccessControl.DirectorySecurity
$acl.SetSecurityDescriptorSddlForm('O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)')
if (Test-Path $target) {
    Set-Acl -Path $target -AclObject $acl
} else {
    $null = [IO.Directory]::CreateDirectory($target, $acl)
}
foreach ($file in @('meldnet.exe', 'meldnetd.exe', 'wintun.dll', 'WINTUN-LICENSE.txt')) {
    $source = Join-Path $PSScriptRoot $file
    if (-not (Test-Path $source -PathType Leaf)) { throw "Missing package file: $file" }
    if ((Get-Item $source).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Package files must not be reparse points.' }
    if ($file -eq 'wintun.dll' -and (Get-AuthenticodeSignature $source).Status -ne 'Valid') { throw 'Official signed Wintun DLL required.' }
    Copy-Item -LiteralPath $source -Destination (Join-Path $target $file)
}
$binary = '"' + (Join-Path $target 'meldnetd.exe') + '" --allow-sid ' + $sid.Value
New-Service -Name Meldnet -DisplayName 'Meldnet VPN' -BinaryPathName $binary -StartupType Automatic -Description 'Independent Meldnet VPN daemon' | Out-Null
Start-Service -Name Meldnet
Write-Output 'Meldnet service installed. Run meldnet.exe as the authorized user. Private state belongs to LocalSystem in ProgramData; GUI/TUI exit does not stop the service.'
