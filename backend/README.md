# TieTie 后端（Go）

替代 `server/` 下 Node 脚本的 Go 后端：代理 Qoder 云端会话、校验上传附件、服务前端静态文件，可选 MySQL 落库。

## 目录结构

```
backend/
├── cmd/server/main.go        # 入口：配置 → 数据库(可选) → 路由 → 优雅退出
└── internal/
    ├── config/               # 环境变量配置
    ├── api/                  # HTTP 汇聚层：router.go 集中注册全部路由
    │   ├── router.go         #   路由（公开区/鉴权区）+ 静态文件(SPA)
    │   ├── middleware.go     #   同源防护 / JWT 守卫 / 访问日志 / panic 恢复
    │   ├── response.go       #   统一 {"error":{code,message}} 响应
    │   ├── auth_handler.go   #   注册 / 登录（bcrypt + JWT 签发）
    │   ├── account_handler.go#   me / 邀请码绑定（含并发绑定去重与会话越权校验）
    │   ├── session_handler.go#   消息读写 + 数据库落库
    │   ├── chat_handler.go   #   SSE 实时流转发
    │   └── upload_handler.go #   消息体与附件校验
    ├── auth/                 # 鉴权模块：JWT 签发/解析、bcrypt 密码哈希、上下文用户
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

默认监听 `127.0.0.1:4173`，与 Node 版相同的环境变量（`QODER_ACCESS_TOKEN` /
`QODER_ALLOWED_ORIGIN` / `QODER_DEFAULT_SESSION_ID` / `HOST` / `PORT`），
另加：

| 变量 | 说明 |
|---|---|
| `DB_DSN` | SQLite 数据库文件路径，默认 `tietie.db`（GORM AutoMigrate 自动建表） |
| `STATIC_DIR` | 前端产物目录，默认 `../frontend/dist` |
| `QODER_TIMEOUT_SECONDS` | 上游请求超时，默认 15s |
| `QODER_AGENT_ID` | 绑定新建会话使用的 Agent；留空自动取账号下第一个未归档 Agent |
| `QODER_ENVIRONMENT_ID` | 绑定新建会话使用的运行环境；留空优先取名为 Default 的环境 |
| `JWT_SECRET` | JWT 签名密钥，生产环境必须设置为随机长字符串 |
| `JWT_TTL_HOURS` | 令牌有效期（小时），默认 720（30 天） |

## 账号与绑定

- `POST /api/auth/register {username, password}`：注册即登录，返回 `{token, user, binding}`；密码 bcrypt 哈希，邀请码 8 位（无 0/O/1/I/L）自动生成防碰撞。
- `POST /api/auth/login {username, password}`：登录，返回同上。
- `GET /api/account/me`：JWT 换取当前账号与绑定状态。
- `POST /api/account/bind {code}`：两人 ID 按字典序组成 `bindings` 复合主键；已有绑定直接返回历史会话，否则云端新建会话后落库。并发绑定以先写入者为准，后到者删除自己多建的会话。
- 消息/SSE 接口校验会话属于当前用户的绑定，越权返回 403 `session_forbidden`。

## 测试与构建

```bash
go test ./...          # make test
go vet ./...
go build -o bin/server ./cmd/server   # make build
docker build -t tietie-backend .      # 镜像只含后端，静态文件用卷挂载并设 STATIC_DIR
```

## 前端联调

- **生产**：在 `frontend/` 里 `npm run build` 后直接跑 Go 服务，它自带静态文件服务（SPA 回退，默认读 `../frontend/dist`）。
- **开发**：起 `go run ./cmd/server`（或 `frontend/` 里 `npm start`），再在 `frontend/` 里 `npm run dev`——
  Vite 已配置 `/api` 代理到 `http://127.0.0.1:4173`。

## 与 Node 版的差异

- 对外接口、错误码、SSE 帧格式与 `server/qoder.mjs` 完全一致（有冒烟对照）。
- `.doc` / `.xls` / `.xlsb` 旧版二进制格式暂不支持解析（返回 `document_unsupported`，
  提示转存为 `.docx` / `.xlsx`）；`.docx` / `.xlsx` / `.xlsm` 用纯标准库实现。
- `dto/upload.go` 中的白名单是 `frontend/src/api/upload-types.json` 的副本，修改契约时两边同步。
