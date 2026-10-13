; Build-only NSIS dependency; the installed daemon never launches this installer.
Unicode true
!include "MUI2.nsh"
!include "nsDialogs.nsh"
!include "LogicLib.nsh"
!include "WinVer.nsh"
!include "x64.nsh"
!ifndef PAYLOAD
  !error "PAYLOAD must name the verified package directory"
!endif
!ifndef ARCH
  !error "ARCH must be amd64 or arm64"
!endif
!ifndef OUTPUT
  !error "OUTPUT must name the output executable"
!endif
Name "Meldnet (${ARCH})"
OutFile "${OUTPUT}"
RequestExecutionLevel admin
SetCompressor /SOLID lzma
ShowInstDetails show
BrandingText "Meldnet VPN"
!define MUI_WELCOMEPAGE_TEXT "Install the Meldnet VPN service, desktop/tray app and command-line tools.$\r$\n$\r$\nYou will choose the individual Windows user permitted to control the VPN.$\r$\n$\r$\nThis setup supports fresh installations only. Existing files and services are preserved and require an explicit upgrade."
!insertmacro MUI_PAGE_WELCOME
Page custom AccountPage AccountLeave
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_TEXT "Meldnet is installed in Program Files\Meldnet.$\r$\n$\r$\nOpen meldnet-gui.exe as the authorized user. The VPN service runs independently; quitting the app leaves the service running.$\r$\n$\r$\nNo automatic GUI launch, shortcut or uninstaller is installed. See the packaged README for service removal and upgrades."
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_LANGUAGE "English"
Var AccountInput
Var AllowedSID
Var Dialog

Function .onInit
  ; Silent installation would bypass the mandatory account-selection page.
  IfSilent 0 +3
    SetErrorLevel 2
    Quit
  ${IfNot} ${AtLeastWin10}
    MessageBox MB_ICONSTOP "Meldnet requires Windows 10 or later."
    Quit
  ${EndIf}
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "Meldnet requires 64-bit Windows."
    Quit
  ${EndIf}
  ; NSIS uses IsWow64Process2 so emulation cannot select the wrong driver.
!if "${ARCH}" == "amd64"
  ${If} ${IsNativeAMD64}
    Goto correct_arch
  ${EndIf}
!else if "${ARCH}" == "arm64"
  ${If} ${IsNativeARM64}
    Goto correct_arch
  ${EndIf}
!else
  !error "Unknown architecture"
!endif
  MessageBox MB_ICONSTOP "This setup is for ${ARCH}. Download the package matching your Windows architecture."
  Quit
correct_arch:
FunctionEnd

Function AccountPage
  !insertmacro MUI_HEADER_TEXT "Choose the VPN user" "Only this individual account and LocalSystem may control Meldnet."
  nsDialogs::Create 1018
  Pop $Dialog
  ${If} $Dialog == error
    Abort
  ${EndIf}
  ${NSD_CreateLabel} 0 0 100% 55u "Enter the SID of the user who will run the desktop app (not necessarily the administrator installing it).$\r$\n$\r$\nThat user can find their SID by running whoami /user in a terminal."
  Pop $0
  ${NSD_CreateLabel} 0 62u 100% 12u "&Authorized user SID (S-1-5-21-... or S-1-12-1-...)"
  Pop $0
  ${NSD_CreateText} 0 78u 100% 14u "$AllowedSID"
  Pop $AccountInput
  ${NSD_CreateLabel} 0 103u 100% 34u "Setup starts the independent VPN service. It does not enroll a network or change VPN routes until you configure a connection."
  Pop $0
  nsDialogs::Show
FunctionEnd

Function AccountLeave
  ${NSD_GetText} $AccountInput $AllowedSID
  ; Restrict the complete argument before it reaches a process command line.
  ; The PowerShell installer additionally validates and resolves the user SID.
  StrCpy $0 $AllowedSID 4
  StrCmp $0 "S-1-" 0 invalid_sid
  StrLen $0 $AllowedSID
  IntCmp $0 184 invalid_sid 0 invalid_sid
  StrCpy $1 4
sid_loop:
  StrCpy $2 $AllowedSID 1 $1
  StrCmp $2 "" sid_done
  StrCmp $2 "-" sid_next
  StrCmp $2 "0" sid_next
  StrCmp $2 "1" sid_next
  StrCmp $2 "2" sid_next
  StrCmp $2 "3" sid_next
  StrCmp $2 "4" sid_next
  StrCmp $2 "5" sid_next
  StrCmp $2 "6" sid_next
  StrCmp $2 "7" sid_next
  StrCmp $2 "8" sid_next
  StrCmp $2 "9" sid_next invalid_sid
sid_next:
  IntOp $1 $1 + 1
  Goto sid_loop
invalid_sid:
  MessageBox MB_ICONEXCLAMATION "Enter an individual Windows user SID, using only S- followed by numbers and hyphens."
  Abort
sid_done:
FunctionEnd

Section "Install"
  InitPluginsDir
  ; Protect staged scripts/binaries before extracting them. Non-elevated
  ; processes must not be able to replace the script run by this setup.
  System::Call 'advapi32::ConvertStringSecurityDescriptorToSecurityDescriptorW(w "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)", i 1, *p .r0, p 0) i.r1'
  StrCmp $1 0 staging_failed
  System::Call 'advapi32::SetFileSecurityW(w "$PLUGINSDIR", i 0x80000005, p r0) i.r1'
  System::Call 'kernel32::LocalFree(p r0)'
  StrCmp $1 0 staging_failed
  Goto staging_ready
staging_failed:
  SetErrorLevel 1
  MessageBox MB_ICONSTOP "Cannot protect setup's temporary payload directory. Installation has stopped."
  Abort
staging_ready:
  SetOutPath "$PLUGINSDIR\payload"
  File "${PAYLOAD}/meldnet.exe"
  File "${PAYLOAD}/meldnetd.exe"
  File "${PAYLOAD}/meldnet-gui.exe"
  File "${PAYLOAD}/wintun.dll"
  File "${PAYLOAD}/WINTUN-LICENSE.txt"
  File "${PAYLOAD}/README.md"
  File "${PAYLOAD}/install.ps1"
  ; A 32-bit setup must invoke native PowerShell to select native Program Files.
  ; nsExec has a bounded inactivity timeout and captures sanitized installer output.
  nsExec::ExecToLog /TIMEOUT=120000 '"$WINDIR\Sysnative\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$PLUGINSDIR\payload\install.ps1" -AllowedSID "$AllowedSID"'
  Pop $0
  StrCmp $0 "0" installed
  SetErrorLevel 1
  MessageBox MB_ICONSTOP "Installation did not complete (result: $0). Review the details above. Files or a stopped service may remain; preserve any state and resolve the failure before retrying."
  Abort
installed:
  DetailPrint "Meldnet installed. Open Program Files\Meldnet\meldnet-gui.exe as the authorized user."
SectionEnd
