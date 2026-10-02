.PHONY: dev build tidy vet

dev: ## 本地开发运行
	go run ./cmd/server

build: ## 编译到 bin/server
	go build -o bin/server ./cmd/server

vet: ## 静态检查
	go vet ./...

tidy: ## 整理依赖
	go mod tidy
