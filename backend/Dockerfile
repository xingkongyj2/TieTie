# 构建: 在 backend/ 目录下 docker build -t tietie-backend .
# 说明: 前端产物 dist/ 不在构建上下文内，镜像只含后端二进制；
#       部署时通过卷或独立镜像提供静态文件，并设置 STATIC_DIR。
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/server /app/server
ENV HOST=0.0.0.0 PORT=4173 STATIC_DIR=/app/dist
EXPOSE 4173
ENTRYPOINT ["/app/server"]
