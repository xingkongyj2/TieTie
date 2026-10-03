# 贴贴 · 两个人的小窝

面向手机的 React + TypeScript + Vite H5 应用。聊天与提醒接入 Qoder Cloud Agents 的 Managed Session 和 Go 后端。双人身份、个人资料、共享提醒、纪念日、倒计时与定时队列保存在服务端，并按业务规则同步云端记忆；部分界面偏好保存在当前设备。

## 运行

仓库分两个模块：`frontend/`（React + Vite）与 `backend/`（Go 服务）。前端需要 Node.js 22.12+，后端需要 Go 1.22+。

```bash
cd frontend
npm install
cd ../backend
cp .env.example .env.local   # 仅首次配置；已有 .env.local 时不要覆盖
```

在 `backend/.env.local` 中填写服务端配置：

```dotenv
QODER_ACCESS_TOKEN=你的令牌
QODER_DEFAULT_SESSION_ID=可选的已有会话ID
```

两个终端分别启动（npm 命令都在 `frontend/` 下执行）：

```bash
npm start      # 即 cd ../backend && go run ./cmd/server，Go 后端监听 127.0.0.1:4173
npm run dev    # Vite 开发服务，/api 自动代理到 Go 后端
```

打开终端显示的地址（默认 `http://127.0.0.1:5173`）。令牌仅由 Go 后端读取，禁止加 `VITE_` 前缀；`.env.local` 已被忽略，不应提交或发送给浏览器。修改配置后重启后端。

```bash
npm run build   # TypeScript 检查并构建到 frontend/dist/
npm run preview # 预览产物，/api 同样代理到 Go 后端
```

生产部署时 Go 后端直接托管 `frontend/dist/`（`STATIC_DIR`，默认 `../frontend/dist`），单进程即可，无需 Vite。端口占用等环境变量见 `backend/README.md`。

## 登录、邀请码与绑定

- **注册/登录**：用户名（2-24 位中文/字母/数字）+ 密码（≥6 位，数据库明文存储）；登录成功签发 JWT（默认 30 天），前端保存后所有需登录的 `/api` 请求携带 `Authorization: Bearer`，401 时自动回到登录页。旧账号的 bcrypt 哈希在首次成功登录时转换为明文密码。
- 注册后生成 **4 位数字邀请码**（`0000-9999`，撞车时注册流程自动重试；旧格式 8 位字母码已废弃，需要清库重来）。未绑定时显示绑定引导页：可复制邀请码或分享链接（`?invite=CODE`，微信里发给对方，打开自动预填）；输入框只收数字、最长 4 位。
- **绑定**：输入对方邀请码 → 校验双方都未与其他人绑定；已有当前绑定幂等返回；新绑定创建独立记忆仓库、写入全部 JSON 模板，再新建专属会话（agent/环境自动探测，可用 `QODER_AGENT_ID` / `QODER_ENVIRONMENT_ID` 指定）后落库。每人只能有一个绑定对象。
- **配对标题**：新建会话的标题为 `TieTie-<小ID>-<大ID>`（两个用户 ID 按数值排序），标题仅用于展示；每次新初始化都创建独立 Session 和 Memory Store，不按标题复用旧空间。
- 绑定后聊天页固定使用该共享会话；**会话接口按绑定关系鉴权**，非绑定双方访问返回 403。双方并发绑定只发布一个有效空间（后到者清理本次多余会话和仓库）。
- **退出当前会话**：「我的」页底部按钮 → 确认后删除绑定记录（原记录移入 `archived_bindings`），双方同时回到绑定引导页；云端会话与其聊天记录都不删除。重新绑定同一人会创建新的会话和记忆空间，不合并旧空间的数据。
- 数据存于 MySQL（连接参数为 `MYSQL_HOST` / `MYSQL_PORT` / `MYSQL_USER` / `MYSQL_PASSWORD` / `MYSQL_DATABASE`，默认库名 `tietie`），服务启动时自动建库建表，排序规则固定 `utf8mb4_bin`。表模型与增删改查合并在 `backend/internal/dbop/`（每表一文件）。解绑的原绑定记录移入 `archived_bindings`；「一人一绑定」由绑定事务内对两个用户行加锁后复查保证。

## 云端会话

- 绑定成功后自动加载两人共享的专属会话，按事件 ID 去重、按本地日期显示。Agent 回复按 Markdown 展示标题、清单、链接、代码块和表格；宽表可在消息内横向滚动，提醒内容以提示卡片显示。工具调用及思考事件不作为聊天正文。
- Agent 调用客户端自定义工具 `AskUserQuestion` 时，该事件转成 `kind: ask` 消息渲染成选择题卡片；点选项即通过 `POST /api/qoder/sessions/:id/tool-result` 回传 `user.custom_tool_result`，挂起的那一轮继续。等待应答期间云端会话状态仍是 `idle`，此时直接发消息会被上游拒 409（界面表现为"会话忙"），所以看到未作答的卡片要先作答。其他名称的自定义工具应用内无法应答，不进聊天正文。
- 发送内容先由双人会话层包装：真实账号 ID、成员名字、实际发言者、上海时区及当前时间随原话一起转发。浏览器不能指定发言者；同一消息在双方设备上分别显示为自己或对方，无法辨认的旧消息显示历史成员。
- AI 输出由会话层拆成自然语言正文、接收人和提醒操作；完整回复通过校验后才显示，协议 JSON、内部唤醒及思考内容不进入聊天。普通聊天照常进行，明确的提醒请求才由 AI 提取时间与对象；模糊时间先澄清。目前支持一次性提醒。
- 忙碌时每 3 秒、空闲时每 15 秒增量同步，并通过携带 JWT 的 fetch SSE 接收事件。完成本轮后恢复发送，发送失败保留草稿。
- 认证失败、限流、会话忙碌、网络中断等情况显示错误。发送超时时可能已被云端接受，应先刷新确认后再决定是否重发。
- 聊天框可添加 PNG/JPEG/WebP/GIF 图片，也可上传 Qoder Files API 支持的文本类文件：任意 `text/*` MIME、文档列出的 `application/*` 文本 MIME、代码/配置扩展名及 `Dockerfile`、`Makefile` 等无扩展名文件。Excel（`.xlsx/.xlsm`）和 Word（`.docx`）由 Go 后端提取文字和工作表数据，再作为文本资源挂载到当前 Session（旧版 `.doc/.xls/.xlsb` 暂不支持，需转存为新格式）；前端不做任何解析，原文件直接交给后端。图表、图片及宏不会作为原文件交给 Agent。PDF、音视频和压缩包仍不支持。图片通过消息图片块发送，不走 Files 上传接口，聊天中的图片可点击放大。[Qoder Files 支持清单](https://docs.qoder.cn/cloud-agents/files-schemas#%E6%94%AF%E6%8C%81%E4%B8%8A%E4%BC%A0%E7%9A%84%E6%96%87%E4%BB%B6%E7%B1%BB%E5%9E%8B)。

前端默认通过 Vite 代理调用 `/api`；后端 API 允许跨域访问（除注册/登录外均需 JWT）：

| 本地接口 | 鉴权 | 用途 |
| --- | --- | --- |
| `POST /api/auth/register` | 公开 | 注册并登录，返回 JWT、用户与邀请码 |
| `POST /api/auth/login` | 公开 | 登录，返回 JWT 与当前绑定 |
| `GET /api/account/me` | JWT | 当前账号与绑定状态 |
| `POST /api/account/bind` | JWT | 用对方邀请码绑定，返回共享会话 ID |
| `POST /api/account/unbind` | JWT | 退出当前会话：解除双方绑定，云端会话保留 |
| `GET /api/qoder/sessions/:id/messages` | JWT+绑定 | 完整/增量历史和会话状态 |
| `POST /api/qoder/sessions/:id/messages` | JWT+绑定 | 发送文字、图片和文本附件 |
| `POST /api/qoder/sessions/:id/tool-result` | JWT+绑定 | 回答 Agent 抛出的选择题（回传 `user.custom_tool_result`） |
| `GET /api/qoder/sessions/:id/stream` | JWT+绑定 | SSE 实时事件流 |
| `GET /api/qoder/sessions/:id/reminders` | JWT+绑定 | 查看共享提醒 |
| `POST /api/qoder/sessions/:id/reminders` | JWT+绑定 | 明确时间与接收人后创建一次性提醒 |
| `PATCH /api/qoder/sessions/:id/reminders/:reminderId` | JWT+绑定 | 完成、撤销未到期的完成或取消提醒 |

代理仅接受上述接口，剔除 Session 中的环境变量、资源凭据、系统指令等字段。默认只监听本机；手机局域网测试需显式设置 `HOST`。API 对所有来源开放 CORS，且密码以明文保存在本地数据库；公开部署前需自行增加访问与数据保护措施。

Qoder 接口依据：[Session 使用说明](https://docs.qoder.cn/cloud-agents/sessions)、[获取 Session](https://docs.qoder.cn/cloud-agents/sessions-get)、[事件历史](https://docs.qoder.cn/cloud-agents/api/sessions/events/list)、[发送事件](https://docs.qoder.cn/cloud-agents/api/sessions/events/send)。

## 本地功能

- 底部菜单：**我们**（聊天）、**待办**（待办、倒计时、纪念日、贴贴四个标签）、**我的**（个人资料与退出登录）。
- 小窝详情：切换 AI 伙伴与对方；AI 只设置说话方式，对方只编辑小备注，个人资料在“我的”编辑。
- 我的：可编辑小窝昵称、性别、生日、爱好、备注和头像。
- 角色图鉴：十个透明 PNG 角色，保存后更新聊天头像。
- 提醒板：在聊天中自然地说“半小时后提醒我喝水”“明天早上九点提醒我们出门”，或使用快捷表单明确时间与 @全部 / @我 / @他。共享提醒保存到 MySQL，双方看到同一状态；接收人可以完成任务，尚未到期且未投递过的完成可撤销，也可以取消。
- 小纪念：在一起天数和双人纪念卡。

个人资料与 AI 说话方式由服务端保存并同步云端记忆；双人成员身份与提醒协议由服务端随每轮消息传入。共享提醒双方可见，聊天另支持仅自己可见的独立私聊。尚未接入微信登录或离线设备推送。

`localStorage` 的 `tietie.relationship.v1` 保留本地体验数据及部分界面偏好；账号资料、共享纪念日和提醒以服务端记录为准，旧本机提醒不会自动转成定时任务。云端历史不写入浏览器持久存储，旧模拟消息不会混入云端会话。

## 后台定时提醒

服务启动后自动运行 `internal/scheduler`：按 `(status, run_at, id)` 复合索引查 `run_at <= 当前时间` 的提醒，原子批量领取后才查询其空间及接收人，不扫描用户、不为每个用户创建定时器。每个空间同时只唤醒一条提醒，后台云端并发默认上限 4，批量领取默认 64，队列检查默认 1 秒。可配置 `SCHEDULER_POLL_SECONDS` / `SCHEDULER_BATCH_SIZE` / `SCHEDULER_CONCURRENCY`。

Qoder 保留云端已有角色和系统提示词，应用只补充成员身份与后端事件。明确的相对时间请求（如“1分钟后提醒我，去看视频”）在云端接受消息后由后端直接落库，不依赖 AI 的口头承诺；复杂请求通过结构化动作保存。

发言后的 AI 回复通过持久化同步队列处理，即使页面已关闭，提醒操作也会落库。同步游标与实际发言者上下文保存到数据库，后续只读取新增云端事件。到期任务发送隐藏的后台唤醒，AI 回复一人或双方；只有真实 AI 回复到达才标为“已提醒”。会话忙时推迟重试，发送结果不明确时标为“待确认”并读历史核对，不自动重复唤醒。重启可恢复队列；解绑停止该空间的待投递任务，重绑不会重建旧请求。

实际提醒回复到达时，`task_status` 变为 `completed` 并保存完成时间，提醒记忆 `reminder_memories` 在同一事务更新。下一轮发给 AI 的上下文会包含最近的真实提醒记忆。页面显示“提醒已保存到后台”和“已提醒 · 定时任务完成”；用户勾选事项完成仍独立记录。

后端控制台打印启动及任务进展，同时写入 `backend/logs/system.log` 和 `backend/logs/scheduler.log`，每个文件 10 MiB 轮转、保留 5 个备份。调度器每 30 秒报告运行状态与下一任务。可配置 `LOG_DIR` / `LOG_LEVEL`，详见 [后端说明](backend/README.md)。修改源代码后需重启 Go 进程。

当前使用单实例 Go 服务与 MySQL。服务必须持续运行才能及时唤醒；应用内实时提示支持打开页面的用户，关闭页面后可在重新打开时查看提醒，尚无微信/系统离线推送。多副本部署需另外增加分布式任务租约和跨实例会话锁；到期队列的领取已用 MySQL advisory lock 保证同一时刻只有一个进程改写候选行，但会话内的处理仍靠进程内互斥，不代表已实现完整的多副本调度。

## 代码结构

```text
backend/                       Go 后端（结构详见 backend/README.md）
backend/internal/api/          路由汇聚、CORS、SSE 转发、附件校验
backend/internal/qoder/        Qoder 云端客户端与字段映射
backend/internal/dbop/         MySQL 账号、绑定、消息、提醒和任务回执
backend/internal/conversation/ 双人成员身份、消息协议与 AI 提醒动作解析
backend/internal/scheduler/    按到期时间批量领取、限流并发与退出管理
backend/internal/logging/      系统/定时任务独立日志、上海时区、文件轮转
backend/internal/document/     docx/xlsx 文字提取（.doc/.xls 旧格式暂不支持，需转存）
frontend/src/api/upload-types.json 上传白名单契约（前端校验用，后端 dto 有同步副本）
frontend/src/App.tsx           会话选择、消息展示和顶层交互
frontend/src/hooks/useCloudChat.ts 云端消息、发送与轮询状态
frontend/src/api/qoder.ts      浏览器会话 API
frontend/src/api/client.ts     请求与错误处理
frontend/src/hooks/useRelationship.ts 本地资料和提醒状态
frontend/src/api/relationship.ts 本地资料和提醒持久化
frontend/src/components/       聊天、输入栏、资料与工具弹层
```

后续迁移到 Taro 仍需替换 HTML 控件、请求与存储适配器，并适配小程序路由、样式和权限。

## 本次接入验证

- 真实云端会话列表、详情、历史及增量读取均返回 HTTP 200；当前读取到 11 个会话。
- 浏览器验证了会话切换、刷新后保留选择，以及 320 / 375 / 390 / 430px 宽度无页面横向溢出。
- 发送使用隔离的模拟上游验证：正常回复、无文字回复后恢复发送、409 保留草稿、提交成功后同步失败和恢复；未向现有云端会话发送测试消息。
- `npm run build` 通过；前端源码和构建产物未包含令牌。
- 后端已由 Node（server/，已删除）迁移至 Go（backend/）：真实云端会话列表、历史消息、SSE 流及 Vite 代理链路均验证通过，接口与错误码与原 Node 版一致。

双人身份、AI 控制回执、定时提醒和云端长期记忆的逐步流程见 [会话场景说明](docs/conversation-scenarios.md)。

每会话独立 JSON 模板、习惯与长期行动、存储职责、有效期和分页索引见 [记忆系统设计](docs/memory-system.md)。


提醒页支持早安、晚安天气关怀和倒计时。天气仅接入和风天气，需在后端 `.env.local` 配置专属 `QWEATHER_API_HOST` 与 `QWEATHER_API_KEY`；地区未补齐时开关保持关闭，并在群里说明双方填写情况。聊天中可直接修改自己或对方的地区、天气关注指标和倒计时，修改先落库，再通过持久化队列同步已有 JSON 记忆模板。过去的倒计时日期默认每年循环，未来日期默认单次倒数。

天气接口、数据范围与城市推荐依据见 [天气关怀说明](backend/internal/weather/README.md)，独立处理规范见 [天气处理规则](backend/internal/weather/care-policy.txt)。

聊天可直接询问「重新查天气」「我明天要带伞吗」或指定城市；输入框上方的「查天气」按钮使用同一大模型控制流程，即时获取最新天气，默认只查询当前登录用户已填写的地区；明确帮对方查或查双方时才扩大范围。结果以天气卡片展示，详细数据可展开，查询历史同步保存到数据库与动态信息记忆。

## 功能与优化规划

以下汇总产品功能建议与 Qoder Managed Mode 接入方向，均为待规划项，不代表已经实现。当前已具备共享/私聊、一次性提醒、纪念日、倒计时、天气查询与早晚关怀、长期记忆等基础能力。

### 有趣且实用的双人功能

| 功能 | 使用方式 | 实现要点 |
| --- | --- | --- |
| 今天吃什么 | 根据双方口味、忌口和最近吃过的菜给出 3 个选项，双方投票后生成购物清单 | 偏好以已确认资料为准；记录投票与用餐历史，减少重复推荐 |
| 双人心愿池 | 各自记录想去、想吃、想一起做的事，双方共同感兴趣的心愿点亮 | 支持认领、完成与回忆关联；可结合时间、预算和天气推荐周末安排 |
| 空闲时间碰头 | 填写作息或排班，找出双方共同空闲时间 | 明确日期范围和时区，不把未知排班当空闲；双方确认后再创建安排 |
| 约会盲盒 | 选择预算、室内/室外和可用时间，抽取一个约会方案 | 例如「50 元以内的雨天约会」；提供换一个、收藏及加入心愿池 |
| 默契小问答 | 双方独立回答同一问题，全部提交后同时揭晓 | 答案可选择保存为偏好；揭晓前不向另一方展示 |
| 今日电量 | 一键表达很累、想被陪伴或需要独处，并说明希望对方怎么回应 | 由本人主动表达，不由 AI 推断情绪或给关系评分 |
| 共同购物清单 | 随手添加、买完勾选，合并重复物品，也可从菜谱生成 | 双方实时同步，记录购买人，支持数量、分类和备注 |
| 我们的回忆册 | 保存照片和一句话，按时间串成回忆；月底生成可分享的回忆卡 | 手动选择收录内容，区分共享记录与私聊，不自动公开私密内容 |
| 惊喜小助手 | 私聊中根据 TA 已知喜好准备礼物或约会方案 | 安排过程仅自己可见，分享或到期通知只包含用户指定内容 |
| 双人小挑战 | 一起做饭、散步或整理房间，完成后收集徽章 | 支持双方完成、跳过与补记，避免强制打卡和连续天数压力 |
| 徽章系统 | 根据完成事项数、使用天数和共同挑战获得徽章 | 以数据库中的真实完成记录统计，重复操作不重复计数 |
| 语音体验与特效 | 语音输入、消息朗读及可选趣味声音 | 需单独评估语音服务与授权；不把桌面端语音能力当作 Managed API 已具备的能力 |
| 一键服务入口 | 输入框上方提供查天气、吃什么、约会盲盒及未来的咖啡入口 | 按实际已接入的能力展示；未接入服务不提供会误导用户的执行按钮 |

产品优先顺序：先做「今天吃什么 + 双人心愿池」，形成选择、投票、准备、完成的日常使用流程；「约会盲盒」适合作为轻量趣味入口，「空闲时间碰头」可复用已有作息和排班信息。

### 现有功能的优化与 Managed Mode 扩展

| 优先级 | 方向 | 当前基础与建议 |
| --- | --- | --- |
| 高 | 原生自定义工具调用 | 当前通过 `tietie.control` JSON 执行动作，客户端自定义工具仅支持提问交互；逐步把创建提醒、保存纪念日、查询天气等接为带 JSON Schema 的正式工具，后台执行后回传结果 |
| 高 | 重复提醒 | 普通提醒目前只支持一次性；在现有 Go 调度器中扩展每周、每月等规则，提供暂停、修改、下次执行时间和执行历史 |
| 高 | 停止回复 | 接入取消当前 Session turn 的接口；回复中显示停止按钮，共享会话记录是谁停止了本轮 |
| 中 | 图片与文件交付 | 利用 `ImageGen`、`DeliverArtifacts` 生成纪念日贺卡、双人壁纸、旅行计划和行程表；补齐生成文件展示、下载及会话归属校验 |
| 中 | 联网约会规划 | 利用 `WebSearch` / `WebFetch`，结合双方地区、天气、预算和喜好规划活动；展示来源，核对营业时间等容易变化的信息 |
| 中 | 记忆管理页 | 展示「贴贴记住了什么」、来源及修订记录，支持更正、删除；可从已有记忆索引与版本记录继续扩展 |
| 中 | Webhook 辅助同步 | 云端回到 idle 时通知后台拉取结果，减少持续轮询；保留增量游标和轮询补漏，按事件 ID 去重，接收端需可被云端访问 |
| 中 | 已有空间能力更新 | 新增工具、MCP 或 Skill 时显式更新已有 Session，避免只更新 Agent 后旧空间仍使用旧快照；串行更新并核对返回配置与错误事件 |
| 中 | 瑞幸等 MCP 服务 | 核实实际服务地址、可用工具与认证方式；分别绑定双方账号，查询后先展示具体订单，再由用户确认下单 |
| 后续 | Vault 凭证管理 | 按用户、服务和权限范围管理第三方认证；凭证不写进聊天正文或长期记忆，提供断开连接与重新授权入口 |
| 后续 | Dreams 记忆整理 | 先在克隆仓库生成整理结果并预览，校验现有 JSON 模板及数据库事实，再选择合并；不直接替换当前记忆仓库 |
| 后续 | 场景 Skills | 将约会规划、贺卡生成、回忆整理等专业流程拆成按需使用的 Skill，保留统一身份和存储规则；先确认账号能力已开放 |

工具迁移需保留现有权限校验、幂等回执、数据库事务和记忆同步队列。操作界面应显示「创建中 → 已保存」或明确失败，成功确认依据实际执行回执。

技术优先顺序：先做「重复提醒 + 原生工具调用」，再补停止回复与文件交付；Webhook、已有 Session 能力更新和第三方认证作为后续扩展基础。

### 接入边界与参考文档

- 贴贴使用 **Managed Mode**。自然语言 Schedule 及 IM Channel 文档属于 **Forward Mode**，使用 Template、Identity 等资源，不能直接套用到当前 Managed Session；普通重复提醒优先沿用 Go 后台调度。参见 [自然语言 Schedule](https://docs.qoder.cn/cloud-agents/natural-language-schedule-management)。
- Webhook 是云端到后台的通知，不等于手机离线推送。系统或微信通知需要单独接入，也不能替代本地天气和纪念日队列的执行。
- 只更新 Agent 不会自动修改已经创建的 Session。已有 Session 的工具和 MCP 可通过运行配置更新；模型、系统提示词和 Skill 等字段需按文档检查 Beta 请求头及可用性。参见 [更新 Session](https://docs.qoder.cn/cloud-agents/sessions-update)。
- Skills 和 Browser Use 等能力存在开放条件或 Beta 限制，接入前核实当前账号与文档；不要提前把平台支持写成贴贴已支持。
- 参考：[Managed Agents](https://docs.qoder.cn/cloud-agents/agents-list)、[工具配置](https://docs.qoder.cn/cloud-agents/agent-tools)、[取消当前轮](https://docs.qoder.cn/cloud-agents/sessions-cancel)、[文件交付](https://docs.qoder.cn/cloud-agents/files)、[Webhook](https://docs.qoder.cn/cloud-agents/webhooks)、[Vault](https://docs.qoder.cn/cloud-agents/vaults)、[Dreams](https://docs.qoder.cn/cloud-agents/dreams)、[Skills](https://docs.qoder.cn/cloud-agents/agent-skills)。

当前「贴贴」tab 的纪念日提醒已实现：共享设置默认关闭，开启后每年提前 3 天于 08:00 向双方发送群内提示；按上海时区计算，非闰年 2 月 29 日按 2 月 28 日，支持重启去重，关闭或解绑后停止。安静模式已删除。
