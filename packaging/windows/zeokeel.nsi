; ZeoKeel NSIS installer — DRAFT / NOT BUILT (see INSTALLER-PLAN.md)
; This file is a planning artifact for packaging/windows/INSTALLER-PLAN.md.
; It is NOT compiled in this round and NOT wired into scripts/build.sh.
;
; Usage once makensis is available:
;   makensis -DAPPVERSION=1.2.3 -DCOMMIT=abc1234 -DISTDIR=..\..\dist zeokeel.nsi
;
; Expects dist\zeokeel-windows-amd64.exe and dist\zeokeel-windows-amd64-console.exe
; from `make package-windows`.

!ifndef APPVERSION
  !define APPVERSION "0.0.0-dev"
!endif
!ifndef COMMIT
  !define COMMIT "unknown"
!endif
!ifndef DISTDIR
  !define DISTDIR "..\..\dist"
!endif

Name "ZeoKeel"
OutFile "${DISTDIR}\zeokeel-windows-amd64-setup.exe"
Unicode true
; Per-user install by default; machine-wide needs elevation (see INSTALLER-PLAN).
RequestExecutionLevel user
InstallDir "$LOCALAPPDATA\Programs\ZeoKeel"
InstallDirRegKey HKCU "Software\ZeoKeel" "InstallDir"

Page directory
Page instfiles
UninstPage uninstConfirm
UninstPage instfiles

Section "ZeoKeel (required)" SecMain
  SetOutPath "$INSTDIR"
  File "${DISTDIR}\zeokeel-windows-amd64.exe"
  File "${DISTDIR}\zeokeel-windows-amd64-console.exe"
  File "${DISTDIR}\README-WINDOWS.txt"

  WriteRegStr HKCU "Software\ZeoKeel" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "Software\ZeoKeel" "Version" "${APPVERSION}"
  WriteRegStr HKCU "Software\ZeoKeel" "Commit" "${COMMIT}"

  CreateShortcut "$SMPROGRAMS\ZeoKeel.lnk" "$INSTDIR\zeokeel-windows-amd64.exe"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\ZeoKeel" \
    "DisplayName" "ZeoKeel"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\ZeoKeel" \
    "DisplayVersion" "${APPVERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\ZeoKeel" \
    "UninstallString" "$INSTDIR\uninstall.exe"
SectionEnd

Section "Uninstall"
  ; User data in %USERPROFILE%\.zeokeel is intentionally kept by default;
  ; "remove user data" is a future explicit checkbox (see INSTALLER-PLAN.md).
  Delete "$INSTDIR\zeokeel-windows-amd64.exe"
  Delete "$INSTDIR\zeokeel-windows-amd64-console.exe"
  Delete "$INSTDIR\README-WINDOWS.txt"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\ZeoKeel.lnk"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\ZeoKeel"
  DeleteRegKey HKCU "Software\ZeoKeel"
SectionEnd
