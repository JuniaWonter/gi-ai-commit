.PHONY: build install clean test config

BUILD_NAME=git-ai

build:
	go build -o $(BUILD_NAME)

install: build
	@echo "安装 $(BUILD_NAME)..."
	@mkdir -p $(shell go env GOPATH)/bin
	@cp $(BUILD_NAME) $(shell go env GOPATH)/bin/
	@chmod +x $(shell go env GOPATH)/bin/$(BUILD_NAME)
	@echo "✅ 安装完成"
	@$(MAKE) config

install-local: build
	@echo "安装到当前目录..."
	@cp $(BUILD_NAME) /usr/local/bin/$(BUILD_NAME)
	@chmod +x /usr/local/bin/$(BUILD_NAME)
	@echo "✅ 安装完成"
	@$(MAKE) config

config:
	@mkdir -p $(HOME)/.config/ai-commit
	@if [ ! -f $(HOME)/.config/ai-commit/config.yaml ]; then \
		echo "创建配置文件..."；\
		cp config.example.yaml $(HOME)/.config/ai-commit/config.yaml；\
		echo "✅ 配置文件已创建：$(HOME)/.config/ai-commit/config.yaml"；\
		echo "⚠️  请编辑配置文件填写 DeepSeek API Key"；\
	else \
		echo "✅ 配置文件已存在"; \
	fi

git-alias: install
	@echo "创建 git 别名..."
	@git config --global alias.ai '!$(BUILD_NAME)'
	@echo "✅ 可以使用 git ai commit 命令了"

clean:
	go clean
	rm -f $(BUILD_NAME)

test:
	go test ./...

deps:
	go mod tidy
	go mod download

fmt:
	go fmt ./...

lint:
	go vet ./...

help:
	@echo "可用命令:"
	@echo "  make build      - 编译项目"
	@echo "  make install    - 安装到 GOPATH/bin"
	@echo "  make install-local - 安装到 /usr/local/bin"
	@echo "  make config     - 创建配置文件"
	@echo "  make git-alias  - 创建 git 别名 (git ai commit)"
	@echo "  make clean      - 清理构建文件"
	@echo "  make test       - 运行测试"
	@echo "  make deps       - 下载依赖"
	@echo "  make fmt        - 格式化代码"
	@echo "  make lint       - 代码检查"
