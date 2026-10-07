# TieTie 后端（Go）

替代 `server/` 下 Node 脚本的 Go 后端：代理 Qoder 云端会话、校验上传附件、服务前端静态文件，MySQL 持久化与按时间调度共享提醒。

## 目录结构

```
backend/
├── cmd/server/main.go        # 入口：配置 → 数据库 → 会话/定时工作线程 → 路由 → 优雅退出
└── internal/
    ├── config/               # 环境变量配置
    ├── api/                  # HTTP 汇聚层：router.go 集中注册全部路由
    │   ├── router.go         #   路由（公开区/鉴权区）+ 静态文件(SPA)
    │   ├── middleware.go     #   开放 CORS / JWT 守卫 / 访问日志 / panic 恢复
    │   ├── response.go       #   统一 {"error":{code,message}} 响应
    │   ├── auth_handler.go   #   注册 / 登录（明文密码 + JWT 签发）
    │   ├── account_handler.go#   me / 邀请码绑定（含并发绑定去重与会话越权校验）
    │   ├── session_handler.go#   消息读写 + 数据库落库
    │   ├── chat_handler.go   #   SSE 实时流转发
    │   └── upload_handler.go #   消息体与附件校验
    ├── auth/                 # 鉴权模块：JWT 签发/解析、旧密码哈希校验、上下文用户
    ├── qoder/                # Qoder 云端客户端（上游的一切都在这个包）
    │   ├── client.go         #   HTTP 出口：token、超时、分页、错误映射
    │   ├── session.go        #   会话接口（含创建/删除会话）
    │   ├── catalog.go        #   Agent / 环境列表与自动探测
    │   ├── event.go          #   发消息 / SSE / 流事件转换
    │   ├── file.go           #   文件上传与挂载
    │   ├── types.go          #   上游与公开数据结构、消息脱敏转换
    │   └── errors.go         #   ApiError 与上游状态码映射
    ├── dbop/                 # 数据访问层（GORM）：每表一文件 = 模型对象 + 它的增删改查，
    │   │                     #   字段带 json+gorm 双标签；跨表逻辑放 db.go 公共文件
    │   ├── db.go             #   连接、AutoMigrate、公共工具（跨表查询也放这里）
    │   ├── user.go           #   User 模型 + 用户 CRUD
    │   ├── binding.go        #   Binding 模型 + 绑定 CRUD（PairKey 唯一键）
    │   ├── session.go        #   Session 快照模型 + CRUD
    │   ├── message.go        #   Message 模型 + CRUD
    │   └── file.go           #   File 模型 + CRUD
    ├── document/             # docx/xlsx 提取纯文本（纯标准库）
    └── dto/                  # 上传白名单与大小限制（契约源：frontend/src/api/upload-types.json）
```

依赖方向单向：`api → qoder / dbop / document / dto`；`qoder → document`。

## 运行

需要 Go 1.22+（用了 `net/http` 新路由模式，无 Web 框架）。

```bash
cd backend
cp .env.example .env.local    # 填入 QODER_ACCESS_TOKEN
go mod tidy                   # 首次
go run ./cmd/server           # 或 make dev
```

默认监听 `127.0.0.1:4173`，环境变量包括 `QODER_ACCESS_TOKEN` /
`QODER_DEFAULT_SESSION_ID` / `HOST` / `PORT`，
另加：

| 变量 | 说明 |
|---|---|
| `MYSQL_HOST` | MySQL 地址，必填（留空服务拒绝启动，避免误跑成无数据库模式） |
| `MYSQL_PORT` | 端口，默认 `3306` |
| `MYSQL_USER` | 用户名，默认 `root` |
| `MYSQL_PASSWORD` | 密码 |
| `MYSQL_DATABASE` | 库名，默认 `tietie`；启动时 `CREATE DATABASE IF NOT EXISTS`（utf8mb4 / utf8mb4_bin）并 AutoMigrate 建表 |
| `STATIC_DIR` | 前端产物目录，默认 `../frontend/web/dist` |
| `QODER_TIMEOUT_SECONDS` | 上游请求超时，默认 15s |
| `QODER_MEMORY_ENABLED` | 云端长期记忆默认开启；设为 `false` 暂停同步，待同步数据保留，新建空间返回 memory_required |
| `QODER_AGENT_ID` | 绑定新建会话使用的 Agent；留空自动取账号下第一个未归档 Agent |
| `QODER_ENVIRONMENT_ID` | 绑定新建会话使用的运行环境；留空优先取名为 Default 的环境 |
| `JWT_SECRET` | JWT 签名密钥，生产环境必须设置为随机长字符串 |
| `JWT_TTL_HOURS` | 令牌有效期（小时），默认 720（30 天） |
| `WECHAT_APP_ID` | 微信小程序 AppID，必须与 `frontend/project.config.json` 中的 AppID 一致 |
| `WECHAT_APP_SECRET` | 微信公众平台的小程序 AppSecret，仅保存在后端环境变量中 |
| `WECHAT_TIMEOUT_SECONDS` | 微信登录及订阅消息请求超时，默认 10 秒，范围 1–60 |
| `WECHAT_REMINDER_TEMPLATE_ID` | 微信公众平台选定的订阅消息模板 ID；留空停用微信提醒 |
| `WECHAT_REMINDER_TITLE_KEY` / `WECHAT_REMINDER_TIME_KEY` / `WECHAT_REMINDER_CONTENT_KEY` | 实际模板的标题、时间、内容关键词；默认 `thing1` / `time2` / `thing3`，多余字段显式留空 |
| `WECHAT_REMINDER_TYPE_KEY` / `WECHAT_REMINDER_SOURCE_KEY` | 可选通知类型、消息来源关键词，默认留空；模板 3377 使用 `thing1` / `thing3` |
| `WECHAT_MINIPROGRAM_STATE` | 点击推送打开的版本：`formal`（默认）/ `trial` / `developer` |
| `WECHAT_REMINDER_SUBSCRIPTION_TYPE` | `once`（默认）；仅平台批准的长期模板可用 `permanent` |
| `SCHEDULER_POLL_SECONDS` | 定时队列检查间隔，默认 1 秒，范围 1–60 |
| `SCHEDULER_BATCH_SIZE` | 每批领取任务数，默认 64，范围 1–512 |
| `LOG_DIR` | 日志目录，默认 `backend/` 运行目录下的 `logs/` |
| `LOG_LEVEL` | `info`（默认）/ `debug` / `warn` / `error`；debug 可查看每轮同步和读取请求 |
| `SCHEDULER_CONCURRENCY` | 提醒、回复、控制和记忆队列共享的后台并发数，默认 4，范围 1–32 |

## 账号与绑定

- `POST /api/auth/wechat {code, nickname?, avatarUrl?}`：小程序用 `wx.login` 获取临时 code，并可在用户同意 `wx.getUserProfile` 后提交昵称和头像；服务端通过固定的微信 HTTPS `jscode2session` 接口验证身份，返回 `{token, user, binding, isNewUser}`。展示资料仅作为个人档案保存，拒绝授权或头像不可用时随机使用本地默认头像；首次自动创建新账号和邀请码，`isNewUser=true` 时前端进入资料引导；重复登录使用原账号及绑定。不会自动合并历史账号密码账号。
- 微信身份独立存储在 `wechat_identities`，`(app_id, open_id)` 复合主键保证唯一；用户和身份在同一事务中创建，并发重复登录回滚多余用户后读取已创建账号。用户名为随机值，不含 OpenID；微信账号没有可用于密码登录的密码。客户端传入的 OpenID 或 session key 不作为登录凭据，接口和日志均不包含 AppSecret 或 session key。
- 首次启动更新后的后端会通过 AutoMigrate 新建 `wechat_identities`，无需手动改已有用户。部署时填写 `WECHAT_APP_ID` 和 `WECHAT_APP_SECRET` 并重启后端；缺少配置返回 503 `wechat_login_unavailable`，无效或已使用的 code 返回 401 `invalid_wechat_code`，微信请求超时返回 504 `wechat_login_timeout`，上游故障返回 502 `wechat_login_failed`。小程序需将后端 HTTPS 地址配置为 request 合法域名，后端需可访问 `api.weixin.qq.com`。
- 部署后可向 `/api/auth/wechat` POST 空 JSON `{}` 检查接口：新版应返回 400 `invalid_wechat_code`，不会请求微信或创建账号；若仍返回 404 `unsupported_route`，当前服务尚未更新或请求被转发到旧实例。需要先构建并发布包含新版 Go 后端的镜像，再更新服务器容器；只上传小程序或仅拉取旧镜像不能增加此路由。
- `POST /api/auth/register {username, password}`：注册即登录，返回 `{token, user, binding}`；密码明文存储，邀请码 4 位数字自动生成防碰撞。
- `POST /api/auth/login {username, password}`：登录，返回同上。
- `GET /api/account/me`：JWT 换取当前账号与绑定状态。
- `POST /api/account/bind {code}`：先校验双方没有和其他人绑定，同一对已绑定则返回原会话。数据库触发器防止并发请求让一个人绑定多个对象；冲突返回 409，多建的云端会话会清理。
- 旧数据库启动时自动迁移用户 ID 与绑定关系；重复的历史绑定保留最早记录，其他记录归档到 `archived_bindings`。`users` 表不含 `legacy_password_hash` 列；旧账号哈希暂存 `legacy_passwords` 表，首次成功登录后转存明文密码并删除哈希。旧 JWT 需重新登录。
- 消息/SSE 接口校验会话属于当前用户的绑定，越权返回 403 `session_forbidden`。

## 构建

```bash
go vet ./...
go build -o bin/server ./cmd/server   # make build
docker build -t tietie-backend .      # 镜像只含后端，静态文件用卷挂载并设 STATIC_DIR
```

## 前端联调

「待办 → 贴贴 → 纪念日提醒」保存共享空间偏好，默认关闭。开启后，每年在共享纪念日前 3 天的北京时间 08:00 向双方发送群内提示；2 月 29 日在非闰年按 2 月 28 日。提示复用本地关怀消息历史及增量游标，后台需保持运行，无需打开浏览器。当天 08:00–23:00 开启或新增、更正卡片会检查当天提示，重启不重复发送，也不补发错过日期的提示；关闭或解绑后停止，重新绑定默认关闭。

- **生产**：在 `frontend/web/` 里 `npm run build` 后直接跑 Go 服务，它自带静态文件服务（SPA 回退，默认读 `../frontend/web/dist`）。
- **开发**：起 `go run ./cmd/server`（或 `frontend/` 里 `npm start`），再在 `frontend/web/` 里 `npm run dev`——
  Vite 已配置 `/api` 代理到 `http://127.0.0.1:4173`。

微信小程序在 `frontend/` 下通过 Taro 构建，产物由微信发布。它使用公开 HTTPS API 地址和微信登录接口，登录后继续使用 JWT，无需 Vite 代理；生产请配置 request 合法域名。参见 [迁移说明](../frontend/MIGRATION.md)。

## 与 Node 版的差异

- 对外接口、错误码、SSE 帧格式与 `server/qoder.mjs` 完全一致。
- `.doc` / `.xls` / `.xlsb` 旧版二进制格式暂不支持解析（返回 `document_unsupported`，
  提示转存为 `.docx` / `.xlsx`）；`.docx` / `.xlsx` / `.xlsm` 用纯标准库实现。
- `dto/upload.go` 中的白名单是 `frontend/src/api/upload-types.json` 的副本，修改契约时两边同步。

## 双人会话与定时任务

`internal/conversation` 是无网络、无数据库副作用的协议模块。当前使用 V2：用户消息由 JWT 身份包装，区分两位成员和后台系统；AI 输出纯 JSON 的 `tietie.control` 或 `tietie.message`，两者互斥。控制消息严格校验后入持久化队列，后台执行数据库/云端记忆动作，给 AI 发送隐藏的 `action_result`，AI 再回复可见正文。控制 JSON、系统信封和流式片段不转发给浏览器。固定身份、行为与动作契约放在云端内置提示词，完整文本见 [云端系统提示词](../docs/cloud-system-prompt.txt)；后端只发送当前动态信封。完整格式与逐步场景见 [会话场景说明](../docs/conversation-scenarios.md)。

`internal/scheduler` 只接收持久化队列提供的到期任务，提供批量领取、共享并发预算、任务超时和退出等待。`dbop/reminder.go` 根据 `status=scheduled AND run_at<=now` 查索引，单条 `UPDATE ... RETURNING` 原子领取每批任务，每个空间最多一条正在投递。`run_at` 初始等于用户的 `due_at`，繁忙时只后移执行时间，保留原提醒时间。查出提醒后再通过绑定找到空间与接收人，没有用户轮询或每用户 goroutine。

另一个按 `conversation_pending,next_sync_at` 索引的同步队列保存发送后的 AI 动作，不依赖浏览器轮询。同步游标、原始发言者上下文、轮次版本和下次同步时间持久化，读取新增事件，旧同步任务不能清掉新的发言。完成操作保存稳定成员 ID，避免把“自己/对方”的视角写入数据库。

提醒状态：`scheduled → dispatching → delivered`；这里 `delivered` 仅表示 AI 已在空间中回复，不表示用户已阅读或收到设备推送。会话忙/明确拒绝可延期重试；超时/上游 5xx 标记 `uncertain`，通过历史回复确认后补记投递，不自动重发。无效 AI 回复或明确会话错误可标记 `failed`。完成/取消终止任务，动作回执与提醒同事务提交，历史重放不重复创建；同一请求里同一动作 key 的多条 AI 回复也只执行一次。解绑取消待投递任务，绑定创建时间隔离重绑前的请求。

提醒接口均要求 JWT 和当前空间绑定：

消息 POST 可传 `visibility:"shared"|"private"`（默认 shared）。private 原话、附件、AI 回复和到期前的任务只归发送者可见；为对方/双方设置的提醒到期后才在共享空间发布提醒内容。仅提醒自己始终走私密分支。`private_channels` 记录独立云端 Session 和绑定版本，`space_memory_stores` 对应独立仓库；历史接口聚合当前登录者自己的分支，`GET .../private-stream` 只推送当前成员自己的私密消息。沿用既有云端 Agent，不更改角色和系统提示词；具体过程见会话场景说明第11节。

`POST /api/qoder/sessions/:id/cancel` 接收 `{visibility:"shared"|"private"}`，在消息发送锁释放后请求云端停止当前回合。前端继续等待会话回到 idle，再开放输入；已经发送的用户消息仍保留在聊天记录中。

- `GET /api/qoder/sessions/:id/reminders` 返回 `{reminders}`。
- `POST .../reminders` 接收 `{title,dueAt,recipientIds,recurrence?}`；title 为提醒卡片上的简短大事项，最多 80 个 Unicode 字符，详细上下文应另存记忆。dueAt 为未来第一次触发的 RFC3339 时间；可选 recurrence 支持每天、间隔几天、每周、每月、每年、每周指定星期几及指定多个日历日期。同一事项的多个具体日期使用一个 `recurrence.type=dates` 系列，不拆成多条提醒；recipientIds 必须来自当前空间，返回 `{reminder}`。
- `PATCH .../reminders/:reminderId` 接收 `{status:"completed"|"scheduled"|"cancelled"}`。完成/恢复仅接收者可操作，已到期或已投递的任务不能撤销完成。V2 中手动完成和恢复会在同一事务排入 AI 状态确认；聊天消息以 `source=reminder_update` 区分普通回复，到点主动提醒仍使用 `source=reminder`。
- 消息历史额外包含 `members`、`reminders`、`remindersError`；消息包含 `userId`、`displayName`、`recipientIds`、`source`、提醒回执 ID/错误。原始云端 Events 与提醒操作 JSON 不对外公开。

一次性和重复提醒都由数据库调度；重复规则按 Asia/Shanghai 的日历日期计算，既保留每次触发的历史，也排定下一次。服务需常驻，页面关闭不影响保存与唤醒；微信推送需配置模板并获得用户授权。MySQL 下每类队列的「挑候选 + 改状态」由一把 advisory lock 串起来，跨连接不会重复领取；但多副本运行仍需分布式租约与跨实例空间锁，会话内的串行也还依赖进程内互斥，不能直接运行多个进程各自在启动时恢复相同队列。

## 微信订阅提醒

`GET /api/account/wechat-subscription` 返回 `{enabled, templateId, hasWechatIdentity, remaining, subscriptionType}`；`POST` 接收 `{templateId, result:"accept"|"reject"|"ban", requestId}`。用户身份来自 JWT 和当前 AppID 的微信身份记录，客户端不能指定 OpenID。每次原生授权使用独立 requestId，保存重试复用同一 ID，重复提交不会增加次数；拒绝本次授权不抹除之前已接受的次数。一次性模板每次 accept 增加一次预留额度，长期模板授权成功时 remaining 为 `-1`。

普通 AI/手动提醒实际投递完成、早晚关怀及纪念日报告插入时，在同一事务创建 `wechat_notifications`，按来源和接收人去重。模板未启用时不入队；普通聊天和提醒更新不入队。独立 worker 发送前再次检查绑定版本、实际提醒接收人、当前微信身份和额度；一次性额度原子预留。私密提醒只投递给本人，跳转链接使用共享空间 ID，聊天接口只加载当前账号有权看到的消息。

发送通过固定微信 HTTPS 地址获取并缓存 stable token，再调用订阅消息接口。明确限流或忙碌最多重试三次；授权版本未变时，明确拒绝退回预留次数，微信拒绝授权则作废对应额度。发送期间若用户重新授权，旧错误结果不清除新额度，也不退回旧版本预留次数，保守避免恢复已撤销的授权。发送超时、未知响应或进程在发送中退出标记 uncertain，不重复发送。领取后尚未发送的租约可以恢复，超过 24 小时的积压停止发送。后台队列关闭时也不发送微信消息。

当前选用模板 3377「聊天消息通知」，模板 ID 为 `CInFuaL7JKsYeYecbmanbsZy8TY_31jj77fSZhUH9iM`。按 [`.env.example`](.env.example) 配置：`WECHAT_REMINDER_TITLE_KEY=`（留空），`WECHAT_REMINDER_TYPE_KEY=thing1`，`WECHAT_REMINDER_SOURCE_KEY=thing3`，`WECHAT_REMINDER_CONTENT_KEY=thing5`，`WECHAT_REMINDER_TIME_KEY=time6`。通知类型为「待办到期提醒」或早安/晚安/纪念日提醒，消息来自「贴贴清单」，备注是实际提醒内容（thing 字段最多 20 个字符），消息时间使用北京时间。

在服务器填写 AppID、AppSecret 和上述模板配置，更新后端镜像并重新创建应用容器以加载环境变量。开发/体验版分别使用 `WECHAT_MINIPROGRAM_STATE=developer/trial`；服务实例的 `BACKGROUND_WORKERS_ENABLED=true`，共用生产数据库的本地实例保持 `false`。用手机微信登录小程序，在「我的 → 提醒剩余次数」点击右侧铃铛并允许订阅，确认次数增加，再发送「1分钟后提醒我喝水」，检查任务已保存后退出小程序等待微信通知。实际提醒回复保存后进入微信队列，每 5 秒检查一次；在 `logs/scheduler.log` 中搜索 `wechat.notification_result` / `wechat.notification_failed` 检查发送状态。默认一次授权只可收到一条提醒；长期模板必须事先获得微信批准。mock 测试不代表真实发送已验收。参考 [稳定 token](https://developers.weixin.qq.com/miniprogram/dev/server/API/mp-access-token/api_getstableaccesstoken.html)及[消息发送接口](https://developers.weixin.qq.com/miniprogram/dev/server/API/mp-message-management/subscribe-message/api_sendmessage.html)。

## 提醒落库、记忆与运行日志

Qoder 已在云端配置角色和系统提示词，应用保留该人设。首轮或协议更新时初始化完整补充协议，并同步到 `rules/assistant-behavior.json` 的 instructions 字段。后续只补充当前消息、多人成员身份、后台时间和变更的提醒/记忆索引；`conversation_protocols` 保存在数据库，重启后继续增量发送。旧版本身份协议仍可读取。

生产 V2 由 AI 判断是否建立提醒或长期记忆，后台只执行有效控制消息。数据库成功但云端写入失败时发送部分成功回执，并保留持久化同步任务；AI 不能提前承诺两边都保存成功。仅记忆操作不创建提醒，所有事实按7种 JSON 模板合并分页，数据库保留事实及更正历史，云端保存相同模板页面。`memory_only` 作为兼容值接受，实际按 `database_and_memory` 保存；同步成功后清除临时 pendingContent。提醒创建、取消、实际提醒完成和事项完成均更新云端记忆。V2 不绕过 AI 判断。聊天页只显示自然正文与必要失败提示，任务只在提醒页展示，@成员直接在正文中显示整体背景标记。

定时执行另有持久化的 `task_status`：`pending → running → completed`。只有 AI 实际提醒回复保存时，才写入 `task_completed_at`；云端仅接受唤醒时仍为 running。原提醒 `status=delivered` 表示已提醒，用户主动勾选事项后才为 `status=completed`。实际活动是否做完和定时提醒是否执行完分别记录。

`reminder_memories` 保存有来源的长期提醒事实：内容、时间、接收人、任务状态、提醒时间、成员完成确认。创建、提醒完成、取消及成员勾选与记忆在同一数据库事务更新；记忆写入失败时任务状态也回滚。提醒上下文最多保留 20 条最近事实，首次发送，后续仅发送变化；不自动把普通聊天变成长期记忆。

后端启动后控制台显示数据库迁移、云端配置是否就绪、监听地址及调度参数。`internal/logging` 同时输出两个独立文件：

- `logs/system.log`：启动、请求、成员消息被接受、数据库及系统错误；`protocol.outbound` 含首轮初始化标记及请求体字符/字节数，便于观察重复输入的开销，不等同于云端 token 账单。
- `logs/scheduler.log`：任务保存、到期领取、唤醒、云端接受、实际回复完成、记忆同步、重试及失败。包含空间/任务 ID、接收人、原定时间、尝试次数，便于沿同一任务追踪。

所有时间按 Asia/Shanghai 显示。每 30 秒输出调度器运行状态和下一任务，用一条索引查询读取队列头，不扫描用户或统计全部历史任务；普通空轮询不会逐秒刷屏。日志不包含令牌、密码和聊天正文，SQL 参数不会输出。两个文件分别在 10 MiB 时轮转，各保留 5 个备份。

```bash
# 从 backend/ 运行
 tail -f logs/system.log
 tail -f logs/scheduler.log
```

修改后需要重启 Go 进程；`go run` 不会自动加载修改后的源代码。启动日志应出现 `database.ready` 和 `scheduler.started`，数据库应包含 `reminders`、`reminder_action_receipts`、`reminder_memories`、`control_jobs`、`memory_records`、`memory_operation_receipts`、`space_memory_stores`。旧进程下的口头承诺没有任务记录，不能凭未知身份的旧消息补造提醒。

## 会话独立 JSON 记忆

固定模板通过 `internal/memoryspace/templates/` 编译嵌入。新绑定先创建独立仓库并写入七个 JSON 模板，再创建只读挂载的会话；绑定、仓库映射、模板索引原子提交。解绑后的新绑定不找回旧会话。新增 `GET /api/qoder/sessions/{id}/memories?category=habit&after=<memoryKey>`，仅返回当前空间的分页元数据。习惯更正支持 memoryKey；动态信息带 UTC 有效期，过期退出检索并排队删除。详细结构及限制见 [记忆系统设计](../docs/memory-system.md)。
