# TodoList Agent

**A local-first Windows todo list that turns messy thoughts into an executable list with DeepSeek Harness.**

[English](README.md) · [简体中文](README.zh-CN.md) · [Watch the demo](https://www.bilibili.com/video/BV1PR486wEGL/)

TodoList Agent is a small desktop application built with **Go + WebView2**. It connects to a dedicated **DeepSeek Harness (DSH)** session, so the things you type at the bottom of the window can be understood, split into tasks, and kept in sync with the agent.

The important part is not that it is another todo app. It is a place where you can think out loud: fragments, complaints, links, half-finished plans, and the occasional “I should really do this”. DSH only turns **explicit action intentions** into tasks. The original words stay in a searchable journal, together with the tasks they produced.

<p align="center">
  <img src="docs/main.png" width="72%" alt="TodoList Agent main window" />
</p>

## Demo

<p align="center">
  <a href="https://www.bilibili.com/video/BV1PR486wEGL/">
    <img src="docs/bilibili-cover.png" width="80%" alt="TodoList Agent demo on Bilibili" />
  </a>
</p>

A 60-second walkthrough on Bilibili: starting from an empty list, three rounds of conversation with DSH, task extraction, subtasks, focus, checkbox completion, and the journal / activity panels.

## Why this exists

Most todo apps assume you already know what the task is. Real life usually starts earlier than that: you have a vague thought, a deadline buried in a sentence, or five things mixed into one paragraph.

TodoList Agent moves that first step into the desktop:

- you write the thought where it appears;
- DSH reads it in a real conversation and extracts the action;
- the result comes back as a clean task, with the original quote still attached;
- you can refine, focus, split, drop, or finish it without leaving the window.

Everything runs locally. The app does not take screenshots, simulate a keyboard, or start a second DSH process. It talks to DSH's own local Web API.

## Features

### A real DSH conversation, not a one-way form

- The app discovers the running DSH instance on your machine.
- On first launch it creates a dedicated session named `TodoList · DSH 联动` and reuses it later.
- Messages you send appear in DSH as normal user messages, so the agent sees the full context and can reply.
- DSH can update the list through a local bridge API: add, update, start, complete, drop, reopen, and delete.

### Messy input, explicit tasks

- A message may contain several actions; DSH can create several tasks from one thought.
- Pure emotion, commentary, and link sharing do not have to become tasks.
- When a message contains a deadline (“tonight”, “before Friday”, “this weekend”), the time is written into the **task title**, not buried in notes.
- Reopening or editing a task preserves the original wording and its source.

### One level of subtasks

A task can have subtasks when the work genuinely has steps, for example “prepare a talk” → outline / examples / screenshots.

- Parent tasks show progress such as `2/3`.
- Subtasks have their own status and can be completed or dropped independently.
- Subtasks cannot be nested further; the tree stays at one level.
- Deleting a parent task deletes its subtasks after a confirmation.

### Focus, priority bands, and ordering

- Exactly zero or one top-level task can be focused at a time.
- A focused task gets the full-card highlight, a rose border, and a sage-green bold title.
- Tasks are ordered into **Pinned · Long-term**, **High**, **Normal**, and **Low** bands.
- Dragging a task into another band changes its priority; dragging inside a band changes its order.
- Each task has a small display number (`2.4`) next to the checkbox. DSH can use that number to refer to a task precisely.

### Journal and activity history

Every message sent from the desktop composer is stored as a **journal entry**, even if it never becomes a task.

- Search old thoughts or task titles.
- See which tasks were created from a thought.
- Jump from a task back to its source message.
- Switch to **Activity** to see additions, completions, drops, moves, and their original quotes.

### Built for a long list

- Card view for context and subtasks.
- Compact view for scanning dozens or hundreds of tasks by status and priority.
- Completed and dropped groups collapse by default.
- Tray support and a light warm-paper theme designed to stay on screen for a long time.

<p>
  <img src="docs/compact.png" width="48%" alt="Compact task list" />
  <img src="docs/journal.png" width="48%" alt="Thought journal" />
</p>
<p>
  <img src="docs/settings.png" width="48%" alt="Settings" />
  <img src="docs/task.png" width="48%" alt="Task editor" />
</p>

## Requirements

- **Windows 10/11 (64-bit)**
- **Microsoft Edge WebView2 Runtime** (normally included with Windows 11)
- **DeepSeek Harness** installed and running locally

The Windows app targets **DSH 0.2.x**. The Android app supports Android 8.0 or later on arm64 and x86-64.

## Install

### From a release

Download and run `TodoListAgent-Setup-win64.exe` from [GitHub Releases](https://github.com/Dusk-Hermit/todolist-agent/releases). Setup installs for the current user, creates a Start menu shortcut, and downloads WebView2 if needed. The ZIP release also includes `install.cmd`.

### Portable

`TodoListAgent.exe` is a single executable. Put it anywhere and run it. No installer is required.

## First run

1. Start **DeepSeek Harness**.
2. Launch **TodoList Agent**.
3. The app discovers DSH and selects or creates the dedicated session `TodoList · DSH 联动`.
4. Click **让 DSH 维护这份清单** once and send the onboarding message. This gives DSH the local bridge URL, token, and the task-handling rules.
5. Optionally let the app write the managed block into `AGENTS.local.md`, so a long-running DSH session keeps the rules after compaction.

From then on:

- Type a thought in the composer and press `Enter`.
- DSH reads the message and updates the list through the local API.
- Use the task actions to edit, split, drop, finish, or focus.

## Android and LAN sync

1. Install version 0.3.0 or later on Windows and install `TodoListAgent-android.apk` on each Android device. [GitHub Releases](https://github.com/Dusk-Hermit/todolist-agent/releases)
2. Connect the devices to the same LAN. In **Settings → LAN sync**, generate a pairing code on Windows and paste it into each Android app.
3. Paired devices discover each other and sync while the apps are running. The refresh button scans on demand and reports the result. If multicast is unavailable, enter the PC's LAN IPv4 address on Android. If Windows Firewall asks, allow the app on private networks. Discovery and sync use UDP and TCP port 17891.
4. Set up a DSH session on each Windows computer. Android can remember a preferred computer. If it or its DSH becomes unavailable, Android syncs with another ready computer before sending fresh prompts there. When the preferred computer returns, it receives the synchronized tasks, messages, and reply history.

Paired devices use encrypted connections and exchange changed records. Edits to separate tasks merge. If devices concurrently change the same record, the newer list replaces the older list. Deletion markers keep stale devices from restoring deleted tasks. Discovery follows the IPv4 multicast announcement and reply pattern used by [LocalSend](https://github.com/localsend/localsend).

Android saves messages offline and shows the pending queue in Settings. After reconnecting, the PC waits about five seconds to collect the backlog and sorts the messages it has received by their creation times. It sends one at a time to DSH. New inputs join the pending queue during processing. Devices can still edit local tasks, and they exchange the batch results once DSH finishes.

For multi-PC routing, upgrade Android and every DSH computer to 0.4.0 or later. A prompt awaiting acknowledgement stays assigned to its original computer so that a connection failure cannot replay it on another computer. Each DSH session keeps its own context. Use comma-separated IPv4 addresses when multicast discovery is unavailable.

## Updates

Both apps check GitHub Releases at startup. **Settings → About** also offers a manual check and one-click update. The updater verifies the downloaded package. Windows restarts the app after setup. Android offers optional automatic downloading and installation, subject to the system's permission and confirmation prompts. Install 0.4.0 manually when upgrading from earlier versions.

## Daily use

| Action | Result |
|---|---|
| `Enter` | Send the thought to DSH |
| `Shift+Enter` | New line |
| `Ctrl+N` | Create a local task |
| `Ctrl+,` | Open settings |
| Click the checkbox | Complete / reopen a task |
| Click a task title | Open the task editor |
| Click the target icon | Focus / unfocus a task |
| Drag a task | Reorder, or move it to another priority band |
| Book icon | Open the thought journal |
| Clock icon | Open activity history |
| List / grid icon | Switch card and compact views |

## How it works

### DSH discovery and identity

The app tries DSH addresses in this order:

1. `DSH_WEB_URL`, if set;
2. the last address that worked;
3. `http://127.0.0.1:13848`;
4. a local port scan in `127.0.0.1:13800–13920`, validated with `session/list`.

To authenticate, it reads the `client-connection/browser-session` secret from `%USERPROFILE%\.dsh\.credentials.yaml` and mints the same short-lived session cookie DSH uses for its own Web UI. The secret never leaves the machine and is not written into task data.

### The dedicated session

If no dedicated session is remembered, the app calls `session/create`, renames the new session to `TodoList · DSH 联动`, and selects it. The session's working directory follows the currently running DSH session when possible; otherwise it uses the most recently active session.

Existing conversations are never hijacked. A **New dedicated session** action is available in Settings.

### Sending a thought

The composer sends a normal `session/prompt` request. A hidden marker is appended to the message:

```html
<!-- todo-msg:<id> -->
```

DSH passes it back as `sourceMessageId`, which is how the app links a task, its activity events, and the original journal entry. If DSH omits the marker, the app can still attach newly created tasks to the most recent user message within a six-hour window.

### The reverse bridge

The app listens on `127.0.0.1:17890` (configurable) with a random bearer token. The URL and token are written to `%APPDATA%\TodoListAgent\bridge.json`.

DSH can then call:

```http
GET  /api/todos
GET  /api/journal?limit=20&q=keyword

POST /api/todos
```

Example actions:

```json
{"action":"add","title":"周五前准备下周的分享","priority":"high","sourceMessageId":"..."}
{"action":"update","id":"...","title":"...","status":"doing"}
{"action":"complete","id":"..."}
{"action":"drop","id":"...","reason":"暂时不做了"}
```

`GET /api/todos` includes a display-only `number` field (`2.4`) that DSH can use when the user refers to a task by number.

## Task model

### Status

`todo` → `doing` → `done`

A task can also be `dropped`. Dropping is intentionally different from deleting:

- dropped tasks stay in history;
- they are grouped separately and can be filtered;
- they keep the reason and original words;
- they can be reopened or permanently deleted.

### Notes format

Task notes have a stable shape:

```text
{{current detailed description}}

-------

- {{newest original quote}}
- {{older original quote}}
```

The description is rewritten as the task evolves. Original quotes are only appended, newest first. This keeps the actionable summary at the top while preserving the user's own wording below.

### Display numbers

Numbers are computed from the current order and are never persisted:

- top-level tasks: `1`, `2`, `3`, …;
- subtasks: `2.4`, meaning the fourth subtask of the second top-level task.

Numbers change when tasks move, so DSH should re-read `GET /api/todos` before acting on a number.

## Data and privacy

| Path | Contents |
|---|---|
| `%APPDATA%\TodoListAgent\data.json` | Tasks, journal entries, activity events, preferences, bridge token |
| `%APPDATA%\TodoListAgent\bridge.json` | Local bridge URL and bearer token |
| `%APPDATA%\TodoListAgent\icon.ico` | Runtime window icon |

- Data stays on the local machine.
- The bridge binds to loopback only.
- Credentials are read from DSH's own local credential file and are never included in task text or journal entries.
- The app does not capture the screen, control the keyboard, or automate the DSH UI.

## Build from source

Requirements:

- Go 1.26+
- `rsrc` for embedding the Windows icon
- Windows SDK/WebView2 development environment

```powershell
git clone https://github.com/Dusk-Hermit/todolist-agent.git
cd todolist-agent
.\build.ps1
```

The build script embeds the web UI, icon, and manifest, then writes `dist\TodoListAgent.exe` and `dist\TodoListAgent-win64.zip`.

```powershell
# Also build the Windows installer. Requires Inno Setup 6.
.\build.ps1 -Installer
```

For Android development, open `android/` in Android Studio with JDK 17, Gradle 8.6, Android SDK 34, and NDK 27.0.12077973. On Windows, `gradle -p android :app:assembleDebug` writes the APK to `android/app/build/outputs/apk/debug/`.

## Project layout

```text
app.go        application state, task operations, snapshots
store.go      persistence and migration
dsh.go        DSH discovery, dedicated session, prompt delivery
bridge.go     local loopback API used by DSH
window_windows.go   WebView2 bindings and window lifecycle
win32_windows.go    Windows integration for title bar, icon, and tray
android/      Android Studio project
lan*.go       device discovery, authenticated transport, and sync
replica.go    incremental merge and conflict resolution
web/          embedded HTML/CSS/JS interface
install.ps1   per-user installer
build.ps1     release build
docs/         screenshots
```

## Design choices

- **Local first.** No account, no cloud sync, no background service beyond the loopback bridge.
- **One level of nesting.** Enough structure for real work, without turning the list into an infinite tree.
- **Drop is not delete.** Most abandoned tasks are not mistakes; they are decisions worth keeping.
- **The original words stay.** Summaries are useful, but the source is often the most valuable context months later.
- **A dedicated session.** The agent gets a stable place to work without taking over the conversation you are using.

## Limitations

- Windows only.
- Requires a running local DSH instance.
- One level of subtasks by design.
- The bridge is loopback-only; it is not designed for remote access.
- DSH is a fast-moving project, so internal API changes may require an app update.

## Credits

- Built with [Go](https://go.dev/), [go-webview2](https://github.com/jchv/go-webview2), and [systray](https://github.com/getlantern/systray).
- DSH integration is based on DeepSeek Harness's local web API.
- The app's visual direction is inspired by warm paper, dusty rose, and sage green.

---

If TodoList Agent is useful to you, a star helps. Bug reports and focused pull requests are welcome.
