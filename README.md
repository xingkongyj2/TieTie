# 贴贴 · 两个人的小窝

面向手机的 React + TypeScript + Vite H5 应用。聊天页已接入 Qoder Cloud Agents 的 Managed Session，成员资料、头像、偏好与提醒板仍保存在当前设备。

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
npm test        # Go 后端单元测试（qoder 转换、文档抽取），无真实云端写入
npm run build   # TypeScript 检查并构建到 frontend/dist/
npm run preview # 预览产物，/api 同样代理到 Go 后端
```

生产部署时 Go 后端直接托管 `frontend/dist/`（`STATIC_DIR`，默认 `../frontend/dist`），单进程即可，无需 Vite。端口占用等环境变量见 `backend/README.md`。

## 登录、邀请码与绑定

- **注册/登录**：用户名（2-24 位中文/字母/数字）+ 密码（≥6 位，数据库明文存储）；登录成功签发 JWT（默认 30 天），前端保存后所有需登录的 `/api` 请求携带 `Authorization: Bearer`，401 时自动回到登录页。旧账号的 bcrypt 哈希在首次成功登录时转换为明文密码。
- 注册后生成 **4 位数字邀请码**（`0000-9999`，撞车时注册流程自动重试；旧格式 8 位字母码已废弃，需要清库重来）。未绑定时显示绑定引导页：可复制邀请码或分享链接（`?invite=CODE`，微信里发给对方，打开自动预填）；输入框只收数字、最长 4 位。
- **绑定**：输入对方邀请码 → 校验双方都未与其他人绑定；已有同一对记录直接复用历史会话，否则先按**配对标题**在云端找回历史会话，找不到才新建专属会话（agent/环境自动探测，可用 `QODER_AGENT_ID` / `QODER_ENVIRONMENT_ID` 指定）后落库。每人只能有一个绑定对象。
- **配对标题**：新建会话的标题为 `tietie-<小ID>-<大ID>`（两个用户 ID 按数值排序），它同时是这对绑定找回历史的钥匙。云端列表接口不支持按标题过滤，因此拉全量后本地精确匹配，已归档的同名会话不参与恢复。
- 绑定后聊天页固定使用该共享会话；**会话接口按绑定关系鉴权**，非绑定双方访问返回 403。双方并发绑定只会产生一个会话（后到者自动清理多余会话）。
- **退出当前会话**：「我的」页底部按钮 → 确认后删除绑定记录（原记录移入 `archived_bindings`），双方同时回到绑定引导页；云端会话与其聊天记录都不删除。重新绑定同一人时按配对标题找回原会话，聊天记录继续出现在聊天页。
- 数据存于 SQLite（`backend/tietie.db`，GORM 自动建表），表模型与增删改查合并在 `backend/internal/dbop/`（每表一文件）。旧字符串用户 ID 和绑定记录会在启动时迁移到自增整数 ID；旧密码哈希暂存独立的 `legacy_passwords` 表，首次成功登录后删除。冲突的历史绑定保留最早一条，其余记录移入 `archived_bindings`。旧 JWT 需重新登录。

## 云端会话

- 绑定成功后自动加载两人共享的专属会话，按事件 ID 去重、按本地日期显示。Agent 回复按 Markdown 展示标题、清单、链接、代码块和表格；宽表可在消息内横向滚动，提醒内容以提示卡片显示。工具调用及思考事件不作为聊天正文。
- Agent 调用客户端自定义工具 `AskUserQuestion` 时，该事件转成 `kind: ask` 消息渲染成选择题卡片；点选项即通过 `POST /api/qoder/sessions/:id/tool-result` 回传 `user.custom_tool_result`，挂起的那一轮继续。等待应答期间云端会话状态仍是 `idle`，此时直接发消息会被上游拒 409（界面表现为"会话忙"），所以看到未作答的卡片要先作答。其他名称的自定义工具应用内无法应答，不进聊天正文。
- 发送文字到当前 Session，接收成功后展示云端确认的消息；忙碌时每 3 秒、空闲时每 12 秒增量同步。完成本轮后恢复发送，发送失败保留草稿。
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

代理仅接受上述接口，剔除 Session 中的环境变量、资源凭据、系统指令等字段。默认只监听本机；手机局域网测试需显式设置 `HOST`。API 对所有来源开放 CORS，且密码以明文保存在本地数据库；公开部署前需自行增加访问与数据保护措施。

Qoder 接口依据：[Session 使用说明](https://docs.qoder.cn/cloud-agents/sessions)、[获取 Session](https://docs.qoder.cn/cloud-agents/sessions-get)、[事件历史](https://docs.qoder.cn/cloud-agents/api/sessions/events/list)、[发送事件](https://docs.qoder.cn/cloud-agents/api/sessions/events/send)。

## 本地功能

- 底部菜单：**我们**（聊天）、**提醒**（提醒、纪念日、贴贴三个标签）、**我的**（个人资料与退出登录）。
- 小窝详情：切换 AI 伙伴与对方；AI 只设置说话方式，对方只编辑小备注，个人资料在“我的”编辑。
- 我的：可编辑小窝昵称、性别、生日、爱好、备注和头像。
- 角色图鉴：十个透明 PNG 角色，保存后更新聊天头像。
- 提醒板：在“我们”中用多行输入和 @全部 / @我 / @他记下小事；在“提醒”中查看、完成或撤销完成。旧记录的时间继续显示，当前不触发实际通知。
- 小纪念：在一起天数和双人纪念卡。

资料与 AI 偏好暂未同步到 Qoder，模型行为以云端 Agent 配置为准。Cloud API 未提供双人身份映射，所有 `user.message` 暂统一显示为“我”；尚未接入微信登录、推送或多人实时身份。

本地数据保存在 `localStorage` 的 `tietie.relationship.v1`，会话选择保存在 `tietie.qoder.selectedSession.v1`；云端历史不写入本地持久存储。旧模拟消息不会混入云端会话。

## 代码结构

```text
backend/                       Go 后端（结构详见 backend/README.md）
backend/internal/api/          路由汇聚、CORS、SSE 转发、附件校验
backend/internal/qoder/        Qoder 云端客户端与字段映射
backend/internal/dbop/         MySQL 落库（可选，DB_DSN 留空则关闭）
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
- `npm test`（Go 后端单元测试）及 `npm run build` 通过；前端源码和构建产物未包含令牌。
- 后端已由 Node（server/，已删除）迁移至 Go（backend/）：真实云端会话列表、历史消息、SSE 流及 Vite 代理链路均验证通过，接口与错误码与原 Node 版一致。
