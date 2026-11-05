; ZeoKeel Inno Setup installer — DRAFT / NOT BUILT (see INSTALLER-PLAN.md)
; Planning artifact only: NOT compiled in this round and NOT wired into
; scripts/build.sh.
;
; Usage once iscc is available:
;   iscc /DAppVersion=1.2.3 /DCommit=abc1234 /DDistDir=..\..\dist installer.iss
;
; Expects dist\zeokeel-windows-amd64.exe and dist\zeokeel-windows-amd64-console.exe
; from `make package-windows`.

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif
#ifndef Commit
  #define Commit "unknown"
#endif
#ifndef DistDir
  #define DistDir "..\..\dist"
#endif

[Setup]
AppId={{9A3E4C1F-6B7D-4C2E-9F10-ZEOKEEL0001}
AppName=ZeoKeel
AppVersion={#AppVersion}
AppPublisher=ZeoKeel
DefaultDirName={localappdata}\Programs\ZeoKeel
DefaultGroupName=ZeoKeel
PrivilegesRequired=lowest
DisableProgramGroupPage=yes
OutputDir={#DistDir}
OutputBaseFilename=zeokeel-windows-amd64-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
UninstallDisplayName=ZeoKeel {#AppVersion}
; SignTool=... (deferred: needs Authenticode certificate, see INSTALLER-PLAN.md)

[Files]
Source: "{#DistDir}\zeokeel-windows-amd64.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#DistDir}\zeokeel-windows-amd64-console.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#DistDir}\README-WINDOWS.txt"; DestDir: "{app}"; Flags: ignoreversion isreadme

[Icons]
Name: "{group}\ZeoKeel"; Filename: "{app}\zeokeel-windows-amd64.exe"
Name: "{group}\Uninstall ZeoKeel"; Filename: "{uninstallexe}"

[Registry]
Root: HKCU; Subkey: "Software\ZeoKeel"; ValueType: string; ValueName: "InstallDir"; ValueData: "{app}"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\ZeoKeel"; ValueType: string; ValueName: "Version"; ValueData: "{#AppVersion}"
Root: HKCU; Subkey: "Software\ZeoKeel"; ValueType: string; ValueName: "Commit"; ValueData: "{#Commit}"

[Run]
Filename: "{app}\zeokeel-windows-amd64.exe"; Description: "Launch ZeoKeel"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; User data in %USERPROFILE%\.zeokeel is intentionally kept by default; an
; explicit "remove user data" task is a future addition (see INSTALLER-PLAN.md).
Type: files; Name: "{app}\zeokeel-windows-amd64.exe"
Type: files; Name: "{app}\zeokeel-windows-amd64-console.exe"
