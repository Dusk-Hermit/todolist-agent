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
Filename: "{app}\TodoListAgent.exe"; WorkingDir: "{app}"; Flags: nowait skipifnotsilent runasoriginaluser; Check: AutoLaunchRequested

[Code]
const
  WebViewKey = 'Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}';
  LegacyKey = 'Software\Microsoft\Windows\CurrentVersion\Uninstall\TodoListAgent';

function AutoLaunchRequested: Boolean;
var
  I: Integer;
begin
  Result := False;
  for I := 1 to ParamCount do
    if CompareText(ParamStr(I), '/AUTOLAUNCH') = 0 then
      Result := True;
end;

function OpenProcess(Access: LongWord; Inherit: Boolean; PID: LongWord): THandle;
  external 'OpenProcess@kernel32.dll stdcall';
function WaitForSingleObject(Process: THandle; Timeout: LongWord): LongWord;
  external 'WaitForSingleObject@kernel32.dll stdcall';
function CloseHandle(Handle: THandle): Boolean;
  external 'CloseHandle@kernel32.dll stdcall';

function WaitForOldInstance: Boolean;
var
  PID: Integer;
  Process: THandle;
begin
  Result := True;
  PID := StrToIntDef(ExpandConstant('{param:OLDPID|0}'), 0);
  if PID <= 0 then
    Exit;
  Process := OpenProcess($00100000, False, PID);
  if Process <> 0 then
  begin
    Result := WaitForSingleObject(Process, 30000) = 0;
    CloseHandle(Process);
  end;
end;

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
  if not WaitForOldInstance then
  begin
    Result := 'TodoList Agent is still running. Exit it and run Setup again.';
    Exit;
  end;
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
