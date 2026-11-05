# TodoList Agent

一个用 **Go + WebView2** 写的 Windows 长期桌面清单，和当前正在运行的 **DeepSeek Harness（DSH）会话**双向联动：

- 在桌面输入框里写想法 → 消息进入 DSH 会话，DSH 里能看到完整的工作过程和输出。
- 你的输入可以很杂：想法、计划、微信式自言自语、感慨、转述、链接。DSH 只把**明确的行动意图**提炼成任务；纯情绪不会自动变成待办。
- 任务支持**一层子任务**：一个任务可以拆成多个子 todo，父任务显示完成进度；子任务不能再往下嵌套。
- DSH 可以通过本地接口新增、修改、开始、完成、**丢弃**任务；每条输入原文、时间、关联任务都保存在「想法回顾」里。
- 浅色暖纸配色，灵感来自参考图的灰粉 / 鼠尾草绿 / 米白；任务很多时可以切换**紧凑列表**。
- **打开即用**：首次启动会自动发现本机 DSH，创建一个名为 `TodoList · DSH 联动` 的专用会话，而不是占用你正在聊的会话。
- 不截图、不模拟键盘、不另起 DSH 进程；直接走 DSH 自己的本地 Web API。

![主界面](docs/main.png)

<p>
<img src="docs/compact.png" width="46%" alt="紧凑列表" />
<img src="docs/journal.png" width="46%" alt="想法回顾" />
</p>

<p>
<img src="docs/settings.png" width="46%" alt="设置" />
<img src="docs/task.png" width="46%" alt="任务编辑" />
</p>

## 这一版针对“长期、杂乱输入”做了什么

### 1. 内置任务整理规则

接入说明和 `AGENTS.local.md` 明确要求 DSH：

- 先 `GET /api/todos` 查重；
- 只把「想做、要做、记一下、提醒我、推进某事」提炼成任务；
- 纯感慨 / 吐槽 / 转述 / 链接分享不自动建任务，可以简短回应或问一句；
- 一条消息有多个行动点就拆成多个任务，一个行动点不要拆碎；
- 任务最多一层子任务：`add` 时传 `parentId`；只有当一个任务确实有 2-5 个明确步骤时才拆子任务；
- 用户说「关掉 / 做完了 / 搞定」→ `complete`；
- 用户说「不做了 / 算了 / 放弃 / 搁置 / 丢弃」→ `drop` 并记录原因；
- 只有明确说「删除 / 删掉」才 `delete`；
- 任务 `notes` 保留原话，不要过度改写；
- 回复只汇报实际变更，不要只列计划。

### 2. 一层子任务

父任务下可以建立多个子 todo，适合“准备分享 → 截图 / 大纲 / 例子”这种有明确步骤的事情：

- 父任务显示 `1/4 子任务` 的完成进度；
- 子任务有独立的完成 / 丢弃状态，完成后父任务不会自动完成；
- 子任务不能再建子任务（只支持一层），避免清单变成无限深的树；
- 卡片模式在父任务里展开子任务；紧凑模式用缩进跟随父任务；
- 删除父任务会连同子任务一起删除，删除前会提示数量；
- DSH 通过 `add` 的 `parentId` 创建子任务，界面上的 `+ 添加子任务` 也可以直接建。

### 3. 想法回顾（Journal）

每条通过桌面输入框发出的原文都会单独保存，包括没有变成任务的感慨。侧边栏「想法回顾」支持：

- 按时间倒序查看全文；
- 搜索旧想法或任务标题；
- 看每条想法关联了哪些任务；
- 从任务详情里跳回「来源想法」。

应用发出的接入说明不会进入想法回顾，也不会把 API Token 当作任务的来源文本。

### 4. 来源追踪

普通输入发送时会在消息末尾附带一个 Markdown 隐藏标记：

```html
<!-- todo-msg:<id> -->
```

DSH 会把它作为 `sourceMessageId` 传给本地接口；应用据此把任务、历史事件和原始想法关联起来。即使 DSH 没有传，应用也会在 6 小时内把 Agent 新建的任务回退关联到最近一条用户输入。

### 5. 丢弃而不是删除

`dropped` 是独立状态，不会从历史里消失：

- 卡片模式下排在最底部并淡化；
- 紧凑模式下收进「已丢弃」分组；
- 可按「丢弃」筛选查看；
- 任务详情里保留 `dropReason`，可以重新打开或彻底删除。

### 6. 紧凑列表

工具栏右侧的列表/网格按钮切换视图：

- 一行一个任务，按「进行中 / 待办 / 已完成 / 已丢弃」分组；
- 已完成、已丢弃默认折叠，只显示数量；
- 点击分组标题展开/收起；
- 适合积累几十上百条任务后快速浏览。

### 7. 打开即用：自动发现 DSH + 专用会话

普通用户第一次打开应用时不需要先手动选会话：

1. 应用发现本机 DSH，读取会话列表；
2. 如果没有已保存的目标会话，调用 `session/create` 创建一个新的普通会话；
3. 把它重命名为 `TodoList · DSH 联动` 并选中；
4. 新会话的工作目录优先取“当前正在运行的 DSH 会话”的目录；没有运行中的会话时取最近活跃会话的目录；
5. 之后每次启动都复用这个专用会话，不会占用你正在聊的会话。

如果专用会话被删了，或者你想重新开始，设置页有 **「新建专用会话」** 按钮。开发时如果显式设置了 `DSH_SESSION_ID`，应用会优先使用它，方便调试。

## 连接设计（核心）

DSH 桌面版本质上是一个 Electron 外壳 + Cordis 宿主，Web GUI 在 `http://127.0.0.1:<port>` 上提供本地 API，默认 `13848`。这个 API 只接受由 DSH 自己签发的会话 Cookie。

应用按以下方式接入**当前会话**：

1. **发现 DSH**
   - 依次尝试环境变量 `DSH_WEB_URL`、上次保存的地址、`http://127.0.0.1:13848`。
   - 都失败时并发扫描 `127.0.0.1:13800-13920`，用 `session/list` 验证。

2. **本地身份**
   - 读取 `%USERPROFILE%\.dsh\.credentials.yaml` 里的 `client-connection/browser-session` 签名密钥。
   - 按 DSH 的格式签发一个 `v1` Cookie：`HMAC-SHA256(secret, body)`，绑定 `host:port`，有效期 29 天。
   - Cookie 只在本机使用，不写入 DSH 的浏览器存储，也不依赖 `dsh web` 启动时打印的 token。

3. **拿到专用会话**
   - `POST /api/session/list` 读取所有会话。
   - 优先使用已保存的专用会话；没有时调用 `session/create`，重命名为 `TodoList · DSH 联动` 并选中。
   - `DSH_SESSION_ID` 只在开发调试时作为显式覆盖。
   - 设置页可以在全部会话中切换目标，或点「新建专用会话」。

4. **发送输入**
   - `POST /api/session/prompt`，请求体形如：
     ```json
     {
       "type": "client-request",
       "rpcId": "<uuid>",
       "method": "session/prompt",
       "payload": {
         "args": {
           "request": {
             "requestId": "<uuid>",
             "sessionId": "session-...",
             "mode": "queue",
             "content": [{ "type": "text", "text": "你的想法\n\n<!-- todo-msg:<id> -->" }],
             "clientTimeZone": "Asia/Shanghai"
           }
         }
       }
     }
     ```
   - 消息会作为真实的用户消息进入该会话；DSH 里能看到它，并正常开始一轮工作。
   - 轮询 `session/list` 获取 `running`、轮次、最近一轮的回复摘要，用于桌面状态条。

5. **反向更新清单**
   - 应用在 `127.0.0.1:17890` 起一个只监听本机的小型 HTTP 服务，随机 Bearer Token 鉴权。
   - 地址和 Token 写入 `%APPDATA%\TodoListAgent\bridge.json`。
   - DSH 通过 `POST /api/todos` 执行：
     ```json
     {"action":"add","title":"...","notes":"...","priority":"high|normal|low","parentId":"...","sourceMessageId":"..."}
     {"action":"update","id":"...","status":"todo|doing|done|dropped","dropReason":"..."}
     {"action":"complete","id":"..."}
     {"action":"drop","id":"...","reason":"..."}
     {"action":"reopen","id":"..."}
     {"action":"delete","id":"..."}
     ```
   - `GET /api/journal?limit=20&q=关键词` 用于回看旧想法及关联任务。
   - “让 DSH 管理清单”会发送一次接入说明，并可选择写入工作区的 `AGENTS.local.md`。它是 DSH 的本地指令覆盖文件，不会污染共享的 `AGENTS.md`。

这样，输入方向和输出方向都走 DSH 自己的真实会话，不需要任何 UI 自动化。

## 安装

### 方式一：发布包

发布包作为 [GitHub Release](https://github.com/Dusk-Hermit/todolist-agent/releases/latest) 附件提供；仓库本身不提交 `dist/`、`*.exe`、`*.syso`、`data.json`、`bridge.json` 或个人数据。

1. 下载并解压 `TodoListAgent-win64.zip`。
2. 双击 `install.cmd`，或在 PowerShell 中运行：
   ```powershell
   powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1
   ```
3. 开始菜单出现 **TodoList Agent**。
4. 在“设置 → 应用”中可以看到卸载入口。

安装位置：`%LOCALAPPDATA%\Programs\TodoList Agent`

### 方式二：直接运行

`dist\TodoListAgent.exe` 是单文件程序，复制到任意目录即可运行。需要系统已安装 **Microsoft Edge WebView2 Runtime**（Windows 11 通常自带）。

## 使用

1. 启动 DeepSeek Harness（不需要先手动建会话）。
2. 从开始菜单启动 TodoList Agent。首次启动会自动发现 DSH，创建并选中 `TodoList · DSH 联动` 专用会话。
3. 第一次使用点底部的 **“让 DSH 维护这份清单”**：
   - 会向专用会话发送一次接入说明（带本地 API 地址与 Token）。
   - 可选写入 `AGENTS.local.md`，让 DSH 在长会话或压缩后仍然记得接口。
   - 之后 DSH 会按需 `add / update / complete / drop`，桌面清单即时变化。
4. 在底部输入框写想法，`Enter` 发送，`Shift+Enter` 换行。
5. 想拆步骤时，点任务右侧的 **+** 或子任务区的 **“+ 添加子任务”**；父任务会显示 `1/4` 进度。
6. 点右上角图钉保持置顶；关闭窗口会最小化到托盘。
7. 右上角 **书本图标** 打开「想法回顾」，**时钟图标** 打开「操作记录」。
8. 工具栏的列表/网格按钮切换卡片 / 紧凑模式。
9. 齿轮 → 设置：切换会话、新建专用会话、修改 DSH 地址、查看 Agent 接口、复制 curl 示例、复制诊断信息、退出。

普通用户第一次使用的完整路径：

```text
安装包 -> 运行安装 -> 开始菜单打开
                -> 自动发现 127.0.0.1:13848
                -> 自动创建 TodoList · DSH 联动
                -> 点“让 DSH 维护这份清单”
                -> 开始输入
```

### 适合直接丢给 DSH 的输入

```
今天好累，什么都不想干，先吐槽一下。
对了，浅色主题的紧凑模式还要再调一下；旧的下载任务不做了，丢掉。
周末想去看展，记一下。
```

期望结果：

- 「今天好累」不建任务；
- 新建「调整浅色主题的紧凑模式」；
- 把「旧的下载任务」标记为 `dropped` 并写原因；
- 新建「周末去看展」；
- 两条新任务都能在「想法回顾」里找到来源原话。

带子任务的输入：

```
我想准备下周的分享，需要整理演示截图、写一页大纲，再找两个例子。
这个算一个任务，下面拆成子任务吧。
```

期望结果：一条父任务「准备下周的分享」，加三条子任务；父任务显示 `0/3`，子任务完成后变成 `1/3`；子任务不能再嵌套。

### 快捷操作

| 操作 | 效果 |
|---|---|
| `Enter` | 发送到 DSH |
| `Shift+Enter` | 换行 |
| `Ctrl+N` | 新建本地任务 |
| `Ctrl+,` | 打开设置 |
| `Esc` | 关闭弹层 |
| 点击任务标题 | 编辑任务 |
| 点击左侧圆圈 | 完成 / 重新打开 |
| 点击分组标题 | 展开 / 收起紧凑分组 |

## 数据、保留与隐私

| 路径 | 内容 |
|---|---|
| `%APPDATA%\TodoListAgent\data.json` | 任务、历史、输入、偏好、桥接 Token |
| `%APPDATA%\TodoListAgent\bridge.json` | 本地 Agent API 地址与 Token |
| `%APPDATA%\TodoListAgent\WebView2\` | WebView2 缓存 |
| `%APPDATA%\TodoListAgent\app.log` | 运行日志（超过 4MB 轮转） |
| `<工作区>\AGENTS.local.md` | 可选的 DSH 本地指令块（由“让 DSH 管理清单”写入） |

保留策略：

- 原始想法最多保留 10,000 条；
- 操作事件最多保留 20,000 条；
- 桌面状态只加载最近 50 条输入和最近 1,000 条事件，避免界面被历史拖慢；
- 「想法回顾」按页从本地数据读取，支持搜索和加载更早；
- 接入说明不会进入想法回顾；旧版本已写入的接入说明会在加载时迁移清理。

安全边界：

- DSH 签名密钥只读不写；Cookie 只发往 `127.0.0.1` 的 DSH 端口。
- Agent API 只监听 `127.0.0.1`，使用随机 Token。
- Token 会出现在 `bridge.json` 和接入说明消息里；不要把这些内容发到公开渠道。
- 应用不联网，不上传任何数据。

## 仓库卫生

这个仓库只放源码、脚本、图标和文档截图，不放构建产物或个人运行数据：

- `dist/`、`*.exe`、`*.syso`、`*.zip`：构建产物，已忽略；
- `AGENTS.local.md`：应用在你的工作区生成的本地 DSH 指令，已忽略；
- `data.json`、`bridge.json`、`*.log`：本地任务数据、桥接 Token 和日志，已忽略；
- 截图只截应用窗口本身，保存在 `docs/`，用于 README；
- 发布包通过 GitHub Release 附件分发，不提交进 git。

## 构建

环境：

- Windows 10/11 x64
- Go 1.26+（构建脚本会优先使用 `%LOCALAPPDATA%\go-toolchain\go`，并允许自动下载 toolchain）
- Microsoft Edge WebView2 Runtime
- `rsrc`（首次构建自动安装）

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\build.ps1
```

产物：

- `dist\TodoListAgent.exe`
- `dist\TodoListAgent-win64.zip`

开发调试：

```powershell
# 打开 WebView2 的 DevTools 与右键菜单
$env:TODOLIST_AGENT_DEBUG = "1"
.\dist\TodoListAgent.exe
```

## 目录结构

```
.
├── main.go            # 入口、单实例、日志
├── app.go             # 状态、任务变更、丢弃、历史、想法回顾、AGENTS.local.md
├── store.go           # 数据目录、加载、迁移、原子写入
├── dsh.go             # DSH 发现、Cookie、session/list、session/prompt、轮询
├── bridge.go          # 本地 Agent API（127.0.0.1，含 journal）
├── window.go          # WebView2 窗口、Go↔JS 绑定、事件
├── win32.go           # 置顶、关闭到托盘、窗口尺寸、浅色标题栏、图标、单实例
├── tray.go            # 托盘图标与菜单
├── web/               # 内嵌 HTML/CSS/JS（无外部依赖）
├── assets/            # 图标与 manifest
├── install.ps1/.cmd   # 安装
├── uninstall.ps1/.cmd # 卸载
└── build.ps1          # 构建
```

## 已知限制

- 目前只支持 Windows（WebView2）。
- DSH 必须是本机正在运行的进程；远程 DSH 不在这一版范围内。
- 会话列表接口没有过滤参数，应用会一次性拉取会话并只保留最近 60 个用于选择。
- 如果 DSH 的本地 API 或凭据格式在未来版本变化，需要同步调整 `dsh.go`。
- 筛选和状态计数按**顶层任务**计算；子任务状态通过父任务的 `1/4` 进度体现，不会单独出现在「完成」筛选里。
- 接入说明和 `AGENTS.local.md` 是让模型理解清单接口的约定；不同模型的执行稳定性会有差异。模型仍可能误判“感慨”和“任务”的边界，建议在接入后的前几次使用时观察。
- 长期数据目前使用单个 JSON 文件（带原子写入）；如果未来达到数万条输入，再迁移到 SQLite 会更合适。
- 托盘图标位于 Windows 通知区域，可能被系统折叠到“^”里，可在任务栏设置中固定。
