# 贴贴 · 两个人的小窝

面向手机的 React + TypeScript + Vite H5 应用。聊天页已接入 Qoder Cloud Agents 的 Managed Session，成员资料、头像、偏好与提醒板仍保存在当前设备。

## 运行

使用 Node.js 22.12+。

```bash
npm install
cp .env.example .env.local # 仅首次配置；已有 .env.local 时不要覆盖
```

在 `.env.local` 中填写服务端配置：

```dotenv
QODER_ACCESS_TOKEN=你的令牌
QODER_DEFAULT_SESSION_ID=可选的已有会话ID
```

```bash
npm run dev
```

打开终端显示的地址（默认 `http://127.0.0.1:5173`）。令牌仅由 Node 服务端读取，禁止加 `VITE_` 前缀；`.env.local` 已被忽略，不应提交或发送给浏览器。修改配置后重启服务。

```bash
npm test        # 后端接口、分页、错误处理和访问边界测试，无真实云端写入
npm run build   # TypeScript 检查并构建到 dist/
npm run preview # 预览产物，同时提供会话代理
npm start       # Node 同时托管 dist/ 和 API，默认 127.0.0.1:4173
```

已有端口占用时可用 `PORT=4175 npm start`。`dist/` 单独放到静态托管不会提供会话接口，需要同时运行 Node 服务或移植代理到现有后端。

## 云端会话

- 自动读取可访问的会话列表，在右上角“小窝详情”页底部切换、刷新并记住上次选择；初次使用优先选择 `QODER_DEFAULT_SESSION_ID`。
- 加载完整分页历史，按事件 ID 去重、按本地日期显示。Agent 回复按 Markdown 展示标题、清单、链接、代码块和表格；宽表可在消息内横向滚动，提醒内容以提示卡片显示。工具调用及思考事件不作为聊天正文。
- 发送文字到当前 Session，接收成功后展示云端确认的消息；忙碌时每 3 秒、空闲时每 12 秒增量同步。完成本轮后恢复发送，发送失败保留草稿。
- 认证失败、限流、会话忙碌、网络中断等情况显示错误。发送超时时可能已被云端接受，应先刷新确认后再决定是否重发。
- 聊天框可添加 PNG/JPEG/WebP/GIF 图片，也可上传 Qoder Files API 支持的文本类文件：任意 `text/*` MIME、文档列出的 `application/*` 文本 MIME、代码/配置扩展名及 `Dockerfile`、`Makefile` 等无扩展名文件。Excel（`.xlsx/.xls/.xlsm/.xlsb`）和 Word（`.docx/.doc`）由本地服务提取文字和工作表数据，再作为文本资源挂载到当前 Session；图表、图片及宏不会作为原文件交给 Agent。PDF、音视频和压缩包仍不支持。图片通过消息图片块发送，不走 Files 上传接口，聊天中的图片可点击放大。[Qoder Files 支持清单](https://docs.qoder.cn/cloud-agents/files-schemas#%E6%94%AF%E6%8C%81%E4%B8%8A%E4%BC%A0%E7%9A%84%E6%96%87%E4%BB%B6%E7%B1%BB%E5%9E%8B)。

浏览器只调用同源 `/api/qoder`：

| 本地接口 | 用途 |
| --- | --- |
| `GET /api/qoder/sessions` | 会话列表及默认会话 |
| `GET /api/qoder/sessions/:id/messages` | 完整历史和会话状态 |
| `GET /api/qoder/sessions/:id/messages?after=evt_...` | 增量历史和会话状态 |
| `POST /api/qoder/sessions/:id/messages` | 发送文字、图片和文本附件 |

代理仅接受上述接口，剔除 Session 中的环境变量、资源凭据、系统指令等字段，并拒绝跨站调用。默认只在本机使用；手机局域网测试需显式设置监听地址和 `QODER_ALLOWED_ORIGIN` 为实际访问地址。公开部署前需要在自己的网关完成用户认证和会话访问权限控制；当前应用使用一份服务端令牌，不具备多用户隔离。

Qoder 接口依据：[Session 使用说明](https://docs.qoder.cn/cloud-agents/sessions)、[获取 Session](https://docs.qoder.cn/cloud-agents/sessions-get)、[事件历史](https://docs.qoder.cn/cloud-agents/api/sessions/events/list)、[发送事件](https://docs.qoder.cn/cloud-agents/api/sessions/events/send)。

## 本地功能

- 小窝详情：三位成员的名字、资料、头像，以及 AI 偏好保存。
- 角色图鉴：十个透明 PNG 角色，保存后更新聊天头像。
- 提醒板：新增提醒、时间与成员选择、完成与撤销完成；不触发实际通知。
- 小纪念：在一起天数和双人纪念卡。

资料与 AI 偏好暂未同步到 Qoder，模型行为以云端 Agent 配置为准。Cloud API 未提供双人身份映射，所有 `user.message` 暂统一显示为“我”；尚未接入微信登录、推送或多人实时身份。

本地数据保存在 `localStorage` 的 `tietie.relationship.v1`，会话选择保存在 `tietie.qoder.selectedSession.v1`；云端历史不写入本地持久存储。旧模拟消息不会混入云端会话。

## 代码结构

```text
server/qoder.mjs           Qoder 客户端、字段映射与同源 API
server/qoder.test.mjs      API 边界与分页测试
server/vite-plugin.mjs     开发/预览 API 中间件
server/index.mjs           生产静态资源与 API 服务
src/App.tsx               会话选择、消息展示和顶层交互
src/hooks/useCloudChat.ts  云端消息、发送与轮询状态
src/api/qoder.ts           浏览器会话 API
src/api/client.ts          请求与错误处理
src/hooks/useRelationship.ts 本地资料和提醒状态
src/api/relationship.ts    本地资料和提醒持久化
src/components/           聊天、输入栏、资料与工具弹层
```

后续迁移到 Taro 仍需替换 HTML 控件、请求与存储适配器，并适配小程序路由、样式和权限。

## 本次接入验证

- 真实云端会话列表、详情、历史及增量读取均返回 HTTP 200；当前读取到 11 个会话。
- 浏览器验证了会话切换、刷新后保留选择，以及 320 / 375 / 390 / 430px 宽度无页面横向溢出。
- 发送使用隔离的模拟上游验证：正常回复、无文字回复后恢复发送、409 保留草稿、提交成功后同步失败和恢复；未向现有云端会话发送测试消息。
- `npm test` 的 14 项后端测试及 `npm run build` 通过；前端源码和构建产物未包含令牌。
