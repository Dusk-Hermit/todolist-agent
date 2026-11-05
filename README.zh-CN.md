# TodoList Agent

**一个本地优先的 Windows 桌面清单，把杂乱想法交给 DeepSeek Harness，变成真正能执行的待办。**

[English](README.md) · [简体中文](README.zh-CN.md) · [观看演示](https://www.bilibili.com/video/BV1PR486wEGL/)

TodoList Agent 是一个用 **Go + WebView2** 编写的桌面应用。它连接一个专用的 **DeepSeek Harness（DSH）** 会话，让你可以直接在窗口底部写下此刻的想法，由 DSH 理解、拆解，并通过本地接口同步成任务。

它的重点不是“再做一个待办 App”，而是把“想法刚出现、还没想清楚”的那一步放进桌面：零散的句子、感慨、链接、半成形的计划，都可以先写下来。DSH 只把**明确的行动意图**提炼成任务；原始文字会进入可搜索的想法回顾，并和它产生的任务保持关联。

<p align="center">
  <img src="docs/main.png" width="72%" alt="TodoList Agent 主界面" />
</p>

## 演示视频

<p align="center">
  <a href="https://www.bilibili.com/video/BV1PR486wEGL/">
    <img src="docs/bilibili-cover.png" width="80%" alt="TodoList Agent B站演示视频" />
  </a>
</p>

B站 60 秒功能演示：从空清单开始，连续三轮对话，看 DSH 如何拆任务、处理子任务与专注，以及想法回顾和操作记录。

## 为什么做这个

大多数待办工具都假设你已经知道任务是什么了。但真实情况往往更早一点：你只有一个模糊的念头，一句埋在话里的截止时间，或者一段混杂了五件事的碎碎念。

TodoList Agent 把这一步搬到了桌面上：

- 想法出现时，就在原地写下来；
- DSH 在真实会话里读它，提炼出行动；
- 结果以干净的任务形式回来，原话仍然附着在旁边；
- 你可以继续修改、拆子任务、设专注、丢弃或完成，不用离开这个窗口。

所有数据都在本机。应用不截屏、不模拟键盘、不另起一个 DSH 进程，而是直接调用 DSH 自己的本地 Web API。

## 功能

### 真正的 DSH 会话，而不是单向表单

- 自动发现本机正在运行的 DSH。
- 首次启动时创建并选中专用会话 `TodoList · DSH 联动`，之后持续复用。
- 你发出的内容会作为普通用户消息进入 DSH，Agent 能看到完整上下文并正常回复。
- DSH 可以通过本地桥接接口新增、修改、开始、完成、丢弃、重新打开和删除任务。

### 允许杂乱输入，只提炼明确行动

- 一条消息里有多个行动点，可以拆成多个任务。
- 纯情绪、吐槽、转述和链接分享，不必强行变成任务。
- 想法中有明确时间（今晚、周五前、周末、下周等）时，时间会写进**任务标题**，而不是只藏在备注里。
- 修改任务时保留原始措辞和来源，不把历史抹掉。

### 一层子任务

当一个任务确实有步骤时，可以拆出子任务。例如“准备分享”可以拆成“写大纲 / 找例子 / 整理截图”。

- 父任务显示 `2/3` 这样的完成进度。
- 子任务有独立的完成、丢弃状态，不会自动完成父任务。
- 子任务不能再嵌套，整个清单最多一层。
- 删除父任务会连同子任务一起删除，执行前会提示数量。

### 专注、优先级与排序

- 全局最多只有一个顶层任务处于专注状态，也可以没有。
- 专注任务使用整卡高亮、玫瑰色边框和鼠尾草绿加粗标题。
- 待办按**置顶 · 长期 / 高 / 中 / 低**四个优先级分段。
- DSH 从日常表达提炼的新待办默认追加到对应组末尾。用户明确要求前插时，DSH 可以通过 `placement: "first"` 指定位置。
- 把任务拖到另一段会改变优先级；在同一段内拖动只调整顺序。
- 每个任务在打钩圆圈右侧有很小的显示序号（如 `2.4`），DSH 可以用序号精确指代任务。

### 想法回顾与操作记录

从桌面输入框发出的每条内容都会保存为一条**想法**，即使它没有变成任务。

- 按时间倒序查看原文。
- 搜索旧想法或任务标题。
- 查看一条想法关联了哪些任务。
- 从任务详情跳回来源想法。
- 切换到**操作记录**，查看新增、完成、丢弃、移动等事件及其原话。

### 为长期清单准备

- 卡片视图：保留上下文和子任务展开。
- 紧凑视图：按状态和优先级快速扫几十上百条任务。
- 已完成、已丢弃默认折叠。
- 浅色暖纸主题，适合长时间停留在桌面上。
- 支持托盘运行，关闭窗口不退出。

<p>
  <img src="docs/compact.png" width="48%" alt="紧凑列表" />
  <img src="docs/journal.png" width="48%" alt="想法回顾" />
</p>
<p>
  <img src="docs/settings.png" width="48%" alt="设置" />
  <img src="docs/task.png" width="48%" alt="任务编辑" />
</p>

## 环境要求

- **Windows 10/11（64 位）**
- **Microsoft Edge WebView2 Runtime**（Windows 11 通常自带）
- 本机已安装并运行 **DeepSeek Harness**

桌面端面向 **DSH 0.2.x**。Android 端支持 Android 8.0 及以上的 arm64 和 x86-64 设备。

## 安装

### 使用发布包

从 [GitHub Releases](https://github.com/Dusk-Hermit/todolist-agent/releases) 下载并运行 `TodoListAgent-Setup-win64.exe`。安装器会按当前用户安装、创建开始菜单快捷方式，并在系统缺少 WebView2 时联网补装。也可以解压 `TodoListAgent-win64.zip` 后运行 `install.cmd`。

### 免安装

`TodoListAgent.exe` 是单文件程序，放到任意目录直接运行即可，不需要安装器。

## 首次运行

1. 启动 **DeepSeek Harness**。
2. 启动 **TodoList Agent**。
3. 应用会自动发现 DSH，并选中或创建专用会话 `TodoList · DSH 联动`。
4. 点击一次 **「让 DSH 维护这份清单」**，发送接入说明。它会告诉 DSH 本地接口地址、Token 和任务处理规则。
5. 可以选择写入 `AGENTS.local.md`，让长时间运行的 DSH 会话在上下文压缩后仍然记得这些规则。

之后：

- 在底部输入想法，按 `Enter` 发送。
- DSH 读取消息，通过本地接口更新清单。
- 用任务操作区编辑、拆子任务、丢弃、完成或设专注。

## Android 与局域网同步

1. Windows 和 Android 都安装 0.3.0 或以上版本。Android 安装同一 [GitHub Release](https://github.com/Dusk-Hermit/todolist-agent/releases) 中的 `TodoListAgent-android.apk`。
2. 将设备连到同一局域网。在电脑的 **设置 → 局域网同步** 生成配对码，复制到各 Android 设备并保存。
3. 应用会自动发现和同步已配对设备。点击顶部的刷新按钮可以立即搜索并查看结果。组播被路由器隔离时，可在手机设置中填入电脑的局域网 IPv4 地址。Windows 防火墙提示时允许应用访问专用网络，发现和同步使用 UDP 和 TCP 17891。
4. 每台 Windows 电脑分别连接并初始化自己的 DSH 会话，可在桌面设置修改地址和端口。手机可设置首选电脑。如果它离线或 DSH 不可用，手机会先与另一台可用电脑同步，再把新消息交给它。首选电脑恢复后会收到同步过的任务、消息和回复记录。Android 打开应用时参与同步。

设备间传输采用配对加密。每次同步只传输有变化的记录。不同任务上的修改会合并，同一记录上的并发修改发生冲突时，以最近编辑的整份清单为准。删除会保留同步标记，避免旧设备恢复已删除的任务。发现机制参考了 [LocalSend](https://github.com/localsend/localsend) 的 IPv4 组播通告和应答。

手机离线时可以连续保存多条消息，并在设置里查看待发队列。连接后，电脑先留出约五秒收集积压，再将已收到的消息按各设备创建时间排序，与本机输入一起逐条交给 DSH。后到的消息会加入尚未发出的队列。DSH 批量处理期间继续接收消息和本地任务编辑，清单在本批处理完成后恢复同步。

多电脑代发请将手机和执行 DSH 的电脑都升级到 0.4.0 或更高版本。等待原电脑确认的消息保留原归属，避免超时后在两台电脑重复执行。各电脑的 DSH 会话上下文保存在原电脑。组播受限时可用逗号分隔多个手动地址。

## 更新

Windows 和 Android 启动时自动检查 GitHub Release，也可以在「设置 → 关于」手动检查。发现新版本后，点击「一键更新」即可下载并校验相应安装包。Windows 完成安装后会重新启动应用。Android 可以在同一处开启自动下载并安装，系统可能要求允许安装来源或确认。0.3.0 及更早版本首次升级需要手动安装新版。

## 日常使用

| 操作 | 效果 |
|---|---|
| `Enter` | 发送给 DSH |
| `Shift+Enter` | 换行 |
| `Ctrl+N` | 新建本地任务 |
| `Ctrl+,` | 打开设置 |
| 点击左侧圆圈 | 完成 / 重新打开 |
| 点击任务标题 | 打开任务编辑 |
| 点击靶心按钮 | 设为专注 / 取消专注 |
| 拖动任务 | 调整顺序，或移动到其他优先级段 |
| 书本图标 | 打开想法回顾 |
| 时钟图标 | 打开操作记录 |
| 列表 / 网格图标 | 切换卡片 / 紧凑视图 |

## 工作原理

### 发现 DSH 与本地身份

应用按以下顺序寻找 DSH：

1. 环境变量 `DSH_WEB_URL`；
2. 上次成功使用的地址；
3. `http://127.0.0.1:13848`；
4. 在 `127.0.0.1:13800–13920` 范围内扫描，并用 `session/list` 验证。

认证时，应用读取 `%USERPROFILE%\.dsh\.credentials.yaml` 中 `client-connection/browser-session` 的密钥，按 DSH 自己的格式签出短期会话 Cookie。密钥只在本机使用，不会写入任务数据。

### 专用会话

如果没有已保存的专用会话，应用调用 `session/create` 创建新会话，重命名为 `TodoList · DSH 联动` 并选中。会话工作目录优先跟随当前正在运行的 DSH 会话；没有运行中的会话时，使用最近活跃会话的目录。

应用不会劫持你正在使用的会话。设置页提供 **「新建专用会话」**。

### 发送一条想法

输入框通过 `session/prompt` 发送普通消息。消息末尾会附带一个隐藏标记：

```html
<!-- todo-msg:<id> -->
```

DSH 将它作为 `sourceMessageId` 传回，应用据此把任务、操作事件和原始想法关联起来。即使 DSH 没有传，应用也会在六小时内把新任务回退关联到最近一条用户消息。

### 反向桥接

应用在 `127.0.0.1:17890`（可配置）监听，使用随机 Bearer Token。地址和 Token 写入 `%APPDATA%\TodoListAgent\bridge.json`。

DSH 可以调用：

```http
GET  /api/todos
GET  /api/journal?limit=20&q=关键词

POST /api/todos
```

动作示例：

```json
{"action":"add","title":"周五前准备下周的分享","priority":"high","sourceMessageId":"..."}
{"action":"update","id":"...","title":"...","status":"doing"}
{"action":"complete","id":"..."}
{"action":"drop","id":"...","reason":"暂时不做了"}
```

`GET /api/todos` 的每个任务都带有只用于显示的 `number` 字段（如 `2.4`）。用户用序号指代任务时，DSH 应按这个字段精确匹配。

## 任务模型

### 状态

`todo` → `doing` → `done`

任务也可以进入 `dropped`。丢弃和删除是两件事：

- 丢弃的任务保留在历史里；
- 在列表中单独分组，可筛选；
- 保留丢弃原因和原话；
- 可以重新打开，也可以彻底删除。

### 备注结构

任务备注固定为两段：

```text
{{当前详细描述}}

-------

- {{最新原话}}
- {{较早原话}}
```

详细描述会随着任务变化而重写；原话只追加、不改写，最新的放最上面。这样任务详情上半部分始终是最新、可执行的信息，下半部分才保留历史语气和上下文。

### 显示序号

序号根据当前排序实时计算，不写入数据：

- 顶层任务：`1`、`2`、`3`……
- 子任务：`2.4`，表示第 2 个顶层任务下的第 4 个子任务。

序号会随拖动、优先级和状态变化，所以 DSH 操作前应重新读取 `GET /api/todos`。

## 数据与隐私

| 路径 | 内容 |
|---|---|
| `%APPDATA%\TodoListAgent\data.json` | 任务、想法、操作事件、偏好、桥接 Token |
| `%APPDATA%\TodoListAgent\bridge.json` | 本地桥接地址与 Bearer Token |
| `%APPDATA%\TodoListAgent\icon.ico` | 运行时窗口图标 |

- 数据保存在本机。
- 桥接服务只监听回环地址。
- 凭据读取自 DSH 自己的本地凭据文件，不会进入任务正文或想法回顾。
- 应用不截屏、不控制键盘、不自动化 DSH 界面。

## 从源码构建

需要：

- Go 1.26+
- `rsrc`（用于嵌入 Windows 图标）
- Windows SDK / WebView2 开发环境

```powershell
git clone https://github.com/Dusk-Hermit/todolist-agent.git
cd todolist-agent
.\build.ps1
```

构建脚本会嵌入 Web UI、图标和 manifest，并输出 `dist\TodoListAgent.exe` 与 `dist\TodoListAgent-win64.zip`。

```powershell
# 另需 Inno Setup 6，生成 Windows 安装程序
.\build.ps1 -Installer
```

Android 开发时在 Android Studio 打开 `android/`。使用 JDK 17、Gradle 8.6、Android SDK 34 和 NDK 27.0.12077973。在 Windows 上也可以运行 `gradle -p android :app:assembleDebug`，APK 生成在 `android/app/build/outputs/apk/debug/`。

## 项目结构

```text
app.go        应用状态、任务操作、快照
store.go      持久化与迁移
dsh.go        DSH 发现、专用会话、消息发送
bridge.go     DSH 使用的本地回环接口
window_windows.go   WebView2 绑定与窗口生命周期
win32_windows.go    Windows 标题栏、图标和窗口行为
android/      Android Studio 项目
lan*.go       设备发现、配对传输和同步
replica.go    增量合并与冲突处理
web/          内嵌 HTML/CSS/JS 界面
install.ps1   当前用户安装器
build.ps1     发布构建
docs/         截图
```

## 设计取舍

- **本地优先。** 不需要账号、没有云同步，除了回环桥接没有常驻后台服务。
- **只做一层嵌套。** 足够表达真实步骤，又不会把清单变成无限深的树。
- **丢弃不是删除。** 大多数被放弃的任务不是错误，而是一个值得保留的决定。
- **保留原话。** 摘要很有用，但几个月后，来源往往才是最值钱的上下文。
- **专用会话。** Agent 有稳定的工作空间，同时不占用你正在聊的会话。

## 限制

- 仅支持 Windows。
- 需要本机运行中的 DSH。
- 按设计最多一层子任务。
- 桥接接口只面向本机，不适合远程访问。
- DSH 仍在快速迭代，内部 API 变化可能需要应用跟进。

## 致谢

- 使用 [Go](https://go.dev/)、[go-webview2](https://github.com/jchv/go-webview2) 和 [systray](https://github.com/getlantern/systray) 构建。
- DSH 集成基于 DeepSeek Harness 的本地 Web API。
- 视觉方向受暖纸、灰粉和鼠尾草绿启发。

---

如果 TodoList Agent 对你有用，欢迎点一个 Star。Bug 和聚焦的 Pull Request 都欢迎。
