; OPF Full Windows installer (Inno Setup 6.3+) with GUI wizard.
;
; Build:
;   ISCC.exe /DMyArch=amd64 /DAppVersion=0.4.0 ^
;     /DPayloadRoot=<repo>\dist\installer-payload opf-full.iss
;
; Four installation types: bundle (GUI+TUI+CLI), GUI only, TUI only, CLI only.

#ifndef MyArch
  #define MyArch "amd64"
#endif
#ifndef AppVersion
  #define AppVersion "0.4.0"
#endif
#ifndef Priv
  #define Priv "admin"
#endif
#ifndef PayloadRoot
  #define PayloadRoot "..\..\dist\installer-payload"
#endif

#if MyArch == "amd64"
  #define ArchAllowed "x64compatible"
  #define ArchMode "x64compatible"
#elif MyArch == "arm64"
  #define ArchAllowed "arm64"
  #define ArchMode "arm64"
#else
  #error MyArch must be amd64 or arm64
#endif

[Setup]
AppId={{8E6C0B34-6B0B-4C6A-9F0E-0F4C7A6D2A11}
AppName=Offline Package Fetcher
AppVersion={#AppVersion}
AppVerName=Offline Package Fetcher {#AppVersion}
AppPublisher=Offline Package Fetcher
AppPublisherURL=https://github.com/ph7ti/Offline-Package-Fetcher
AppSupportURL=https://github.com/ph7ti/Offline-Package-Fetcher/issues
AppUpdatesURL=https://github.com/ph7ti/Offline-Package-Fetcher/releases
DefaultDirName={autopf}\OPF
DefaultGroupName=OPF
DisableProgramGroupPage=yes
OutputDir=..\..\dist\installers
OutputBaseFilename=opf-Full-installer-windows-{#MyArch}
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed={#ArchAllowed}
ArchitecturesInstallIn64BitMode={#ArchMode}
PrivilegesRequired={#Priv}
ChangesEnvironment=yes
UninstallDisplayIcon={app}\opf-gui.exe
SetupIconFile=..\..\images\icon.ico
LicenseFile=..\..\LICENSE
SetupLogging=yes
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "brazilianportuguese"; MessagesFile: "compiler:Languages\BrazilianPortuguese.isl"
Name: "spanish"; MessagesFile: "compiler:Languages\Spanish.isl"

[Types]
Name: "bundle"; Description: "Bundle: GUI, TUI and CLI"
Name: "gui"; Description: "GUI only"
Name: "tui"; Description: "TUI only"
Name: "cli"; Description: "CLI only"
Name: "custom"; Description: "Custom"; Flags: iscustom

[Components]
Name: "gui"; Description: "Desktop GUI (opf-gui)"; Types: bundle gui
Name: "tui"; Description: "Terminal UI (opf-tui)"; Types: bundle tui
Name: "cli"; Description: "Command-line interface (opf-cli)"; Types: bundle cli

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut for OPF"; GroupDescription: "Shortcuts:"; Components: gui

[Files]
Source: "{#PayloadRoot}\{#MyArch}\opf-gui.exe"; DestDir: "{app}"; Components: gui; Flags: ignoreversion
Source: "{#PayloadRoot}\{#MyArch}\opf-tui.exe"; DestDir: "{app}"; Components: tui; Flags: ignoreversion
Source: "{#PayloadRoot}\{#MyArch}\opf-cli.exe"; DestDir: "{app}"; Components: cli; Flags: ignoreversion

[Icons]
Name: "{group}\OPF"; Filename: "{app}\opf-gui.exe"; Components: gui
Name: "{group}\Uninstall OPF"; Filename: "{uninstallexe}"
Name: "{autodesktop}\OPF"; Filename: "{app}\opf-gui.exe"; Tasks: desktopicon; Components: gui

[Run]
Filename: "{app}\opf-gui.exe"; Description: "Launch OPF GUI"; Flags: nowait postinstall skipifsilent; Components: gui
Filename: "{cmd}"; Parameters: "/c start ""OPF TUI"" ""{app}\opf-tui.exe"""; Description: "Launch OPF TUI"; Flags: nowait postinstall skipifsilent; Components: tui

[Registry]
Root: HKCU; Subkey: "Environment"; ValueType: expandsz; ValueName: "Path"; ValueData: "{olddata};{app}"; Check: NeedsAddPath('{app}'); Flags: preservestringtype

[UninstallDelete]
Type: filesandordirs; Name: "{app}"

[Code]
function NeedsAddPath(Param: string): Boolean;
var
  OrigPath: string;
begin
  if not RegQueryStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', OrigPath) then
  begin
    Result := True;
    exit;
  end;
  Result := Pos(';' + Uppercase(Param) + ';', ';' + Uppercase(OrigPath) + ';') = 0;
end;

procedure RemoveFromUserPath();
var
  Path, Item, NewPath: string;
  P: Integer;
begin
  if not RegQueryStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', Path) then
    exit;
  NewPath := '';
  while Length(Path) > 0 do
  begin
    P := Pos(';', Path);
    if P = 0 then
    begin
      Item := Path;
      Path := '';
    end
    else
    begin
      Item := Copy(Path, 1, P - 1);
      Delete(Path, 1, P);
    end;
    if (Item <> '') and (CompareText(Item, ExpandConstant('{app}')) <> 0) then
    begin
      if NewPath <> '' then
        NewPath := NewPath + ';';
      NewPath := NewPath + Item;
    end;
  end;
  RegWriteExpandStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', NewPath);
end;

procedure CreateDatabase();
var
  Dir, DB: string;
begin
  Dir := ExpandConstant('{localappdata}\OPF\data');
  ForceDirectories(Dir);
  DB := Dir + '\opf.db';
  if not FileExists(DB) then
    SaveStringToFile(DB, '', False);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
    CreateDatabase();
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
    RemoveFromUserPath();
end;
