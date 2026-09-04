; JARVIS NSIS installer. Compiled by build/pack.ps1.
;
; Copies jarvis.exe into Program Files, registers the Windows service, and
; places a JARVIS shortcut on the Public desktop. The shortcut starts the
; service when it is stopped, or opens the admin UI when it is running.

Unicode true
ManifestDPIAware true
SetCompressor /SOLID lzma
RequestExecutionLevel admin
AllowSkipFiles on

!ifndef VERSION
  !define VERSION "0.1.0"
!endif
!ifndef PAYLOAD
  !define PAYLOAD "."
!endif
!ifndef OUTFILE
  !define OUTFILE "JARVIS-Setup-${VERSION}.exe"
!endif

!include "MUI2.nsh"
!include "LogicLib.nsh"

Name "JARVIS"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\JARVIS"
InstallDirRegKey HKLM "Software\JARVIS" "InstallDir"
BrandingText "JARVIS ${VERSION}"

VIProductVersion "${VERSION}.0"
VIAddVersionKey /LANG=1042 "ProductName" "JARVIS"
VIAddVersionKey /LANG=1042 "FileDescription" "JARVIS 설치"
VIAddVersionKey /LANG=1042 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=1042 "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=1042 "LegalCopyright" "JARVIS"

!define MUI_ICON "${PAYLOAD}\jarvis.ico"
!define MUI_UNICON "${PAYLOAD}\jarvis.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_NOAUTOCLOSE
!define MUI_FINISHPAGE_RUN "$INSTDIR\jarvis.exe"
!define MUI_FINISHPAGE_RUN_TEXT "관리자 UI 열기"

!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "Korean"

Section "JARVIS" SecInstall
  SetOutPath "$INSTDIR"
  SetShellVarContext all

  ; Stop a previous copy so jarvis.exe can be replaced. Failures are normal
  ; on a first install (no service, no leftover process).
  nsExec::ExecToLog 'net stop JARVIS'
  nsExec::ExecToLog 'taskkill /F /IM jarvis.exe'
  Sleep 400

  File "${PAYLOAD}\jarvis.exe"
  File "${PAYLOAD}\jarvis.ico"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  nsExec::ExecToLog '"$INSTDIR\jarvis.exe" install --start=true'
  Pop $0
  ${If} $0 != 0
    DetailPrint "jarvis install exited with $0"
    MessageBox MB_ICONSTOP "Windows 서비스 등록에 실패했습니다. (코드 $0)"
    Abort
  ${EndIf}

  ; Older builds registered a notification-area icon at logon.
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "JARVIS Tray"

  CreateShortCut "$DESKTOP\JARVIS.lnk" "$INSTDIR\jarvis.exe" "" "$INSTDIR\jarvis.ico" 0
  CreateDirectory "$SMPROGRAMS"
  CreateShortCut "$SMPROGRAMS\JARVIS.lnk" "$INSTDIR\jarvis.exe" "" "$INSTDIR\jarvis.ico" 0

  WriteRegStr HKLM "Software\JARVIS" "InstallDir" "$INSTDIR"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\JARVIS" "DisplayName" "JARVIS"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\JARVIS" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\JARVIS" "Publisher" "JARVIS"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\JARVIS" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\JARVIS" "DisplayIcon" "$INSTDIR\jarvis.ico"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\JARVIS" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\JARVIS" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\JARVIS" "NoModify" 1
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\JARVIS" "NoRepair" 1
SectionEnd

Section "Uninstall"
  SetShellVarContext all

  nsExec::ExecToLog '"$INSTDIR\jarvis.exe" uninstall'
  nsExec::ExecToLog 'taskkill /F /IM jarvis.exe'
  Sleep 400

  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "JARVIS Tray"
  Delete "$DESKTOP\JARVIS.lnk"
  Delete "$SMPROGRAMS\JARVIS.lnk"
  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\JARVIS"
  DeleteRegKey HKLM "Software\JARVIS"

  Delete "$INSTDIR\jarvis.exe"
  Delete "$INSTDIR\jarvis.ico"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
SectionEnd
