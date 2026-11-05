#ifndef Version
  #error Pass /DVersion=x.y.z
#endif
#ifndef Payload
  #error Pass /DPayload=path-to-TodoListAgent.exe
#endif
#ifndef WebViewBootstrapper
  #error Pass /DWebViewBootstrapper=path-to-MicrosoftEdgeWebview2Setup.exe
#endif

[Setup]
AppId=TodoListAgent.Inno
AppName=TodoList Agent
AppVersion={#Version}
AppPublisher=TodoList Agent
DefaultDirName={localappdata}\Programs\TodoList Agent
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
MinVersion=10.0
SetupIconFile=..\assets\icon.ico
UninstallDisplayIcon={app}\TodoListAgent.exe
OutputBaseFilename=TodoListAgent-Setup-win64
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "en"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; Flags: unchecked

[Files]
Source: "{#Payload}"; DestDir: "{app}"; DestName: "TodoListAgent.exe"; Flags: ignoreversion
Source: "{#WebViewBootstrapper}"; DestName: "MicrosoftEdgeWebview2Setup.exe"; Flags: dontcopy

[Icons]
Name: "{userprograms}\TodoList Agent"; Filename: "{app}\TodoListAgent.exe"; WorkingDir: "{app}"
Name: "{userdesktop}\TodoList Agent"; Filename: "{app}\TodoListAgent.exe"; Tasks: desktopicon

[Run]
Filename: "{app}\TodoListAgent.exe"; Description: "{cm:LaunchProgram,TodoList Agent}"; Flags: nowait postinstall skipifsilent

[Code]
const
  WebViewKey = 'Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}';
  LegacyKey = 'Software\Microsoft\Windows\CurrentVersion\Uninstall\TodoListAgent';

function HasRuntimeAt(Root: Integer): Boolean;
var
  Version: String;
begin
  Result := RegQueryStringValue(Root, WebViewKey, 'pv', Version)
    and (Version <> '') and (Version <> '0.0.0.0');
end;

function HasRuntime: Boolean;
begin
  Result := HasRuntimeAt(HKLM32) or HasRuntimeAt(HKCU);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ExitCode: Integer;
begin
  Result := '';
  if HasRuntime then
    Exit;
  ExtractTemporaryFile('MicrosoftEdgeWebview2Setup.exe');
  if not Exec(ExpandConstant('{tmp}\MicrosoftEdgeWebview2Setup.exe'),
    '/silent /install', '', SW_HIDE, ewWaitUntilTerminated, ExitCode) or not HasRuntime then
    Result := 'Microsoft Edge WebView2 Runtime installation failed. Connect to the Internet and run Setup again.';
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  LegacyDir: String;
begin
  if (CurStep = ssPostInstall) and RegQueryStringValue(HKCU, LegacyKey, 'InstallLocation', LegacyDir)
    and (CompareText(LegacyDir, ExpandConstant('{app}')) = 0) then
  begin
    RegDeleteKeyIncludingSubkeys(HKCU, LegacyKey);
    DeleteFile(ExpandConstant('{app}\uninstall.ps1'));
  end;
end;
