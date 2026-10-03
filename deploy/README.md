# CNB 部署

与 howbuyyou 一样，向 `main` 推送后，根目录的 `.cnb.yml` 构建 `deploy/dockerfile`，将前后端打进一个镜像并推到 CNB Docker 制品库。镜像是 `docker.cnb.cool/xingkong/my/tietie/tietie:1`，服务监听容器内 `4173` 端口。CNB 构建使用平台注入的 `CNB_TOKEN`，仓库中不保存令牌。

服务器部署（dnf 系统）：

```bash
sudo bash deploy/deploy_docker.sh
cp backend/.env.example backend/.env.local
# 编辑 backend/.env.local：至少填写 QODER_ACCESS_TOKEN、MYSQL_HOST、MYSQL_PASSWORD、JWT_SECRET
CNB_TOKEN=你的CNB访问令牌 bash deploy/cnb_tietie.sh
```

`backend/.env.local` 被 Git 和 Docker 构建上下文排除。部署脚本默认使用当前仓库中的配置文件与日志目录，创建/复用 `howbuyyou_net` 网络，拉取最新镜像并替换名为 `tietie` 的容器。已经在服务器上登录 CNB Docker 制品库时，不必再次传 `CNB_TOKEN`。可用 `ENV_FILE`、`LOG_DIR`、`HOST_PORT`、`NETWORK_NAME`、`CONTAINER_NAME`、`IMAGE` 环境变量覆盖默认值。

MySQL 可通过 `MYSQL_HOST` 指向现有实例，或将 MySQL 容器加入同一个 Docker 网络。生产环境请设置独立的 `JWT_SECRET`。如使用和风天气 JWT，设置 `QWEATHER_PRIVATE_KEY_HOST_FILE` 为服务器上的私钥路径；脚本会只读挂载并覆盖容器内的 `QWEATHER_PRIVATE_KEY_FILE`。
