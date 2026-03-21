BINARY     := axeai
BUILD_DIR  := .
SRC        := ./src/
ZIG_DIR    := tools/filesystem/zig-bindings
ZIG_BIN    := $(ZIG_DIR)/zig-out/bin/filesystem_mcp
ZED_CONFIG := $(HOME)/.config/zed/settings.json
INSTALL_DIR := $(HOME)/.local/bin

AWS_PROFILE ?= default
AWS_REGION  ?= us-east-1
MODEL       ?= us.anthropic.claude-sonnet-4-6-20250219-v1:0

.PHONY: all build zig go install zed clean help

all: build

build: zig go

zig:
	@echo "building zig filesystem binary..."
	cd $(ZIG_DIR) && zig build -Doptimize=ReleaseFast
	@echo "zig binary: $(ZIG_BIN)"

go:
	@echo "building go binary..."
	go build -ldflags="-s -w \
		-X main.defaultProfile=$(AWS_PROFILE) \
		-X main.defaultRegion=$(AWS_REGION) \
		-X main.defaultModel=$(MODEL)" \
		-o $(BINARY) $(SRC)
	@echo "binary: $(BINARY) ($$(du -sh $(BINARY) | cut -f1))"

install: build
	@echo "installing $(BINARY) to $(INSTALL_DIR)..."
	@mkdir -p $(INSTALL_DIR)
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
	@echo "installed: $(INSTALL_DIR)/$(BINARY)"

zed: install
	@echo "configuring Zed..."
	@if [ ! -f "$(ZED_CONFIG)" ]; then \
		echo '{}' > $(ZED_CONFIG); \
	fi
	@node -e " \
		const fs = require('fs'); \
		const cfg = JSON.parse(fs.readFileSync('$(ZED_CONFIG)', 'utf8')); \
		cfg.agent_servers = cfg.agent_servers || {}; \
		cfg.agent_servers['axeai'] = { \
			command: '$(INSTALL_DIR)/$(BINARY)', \
			args: ['--profile', '$(AWS_PROFILE)', '--region', '$(AWS_REGION)', '--model', '$(MODEL)'] \
		}; \
		fs.writeFileSync('$(ZED_CONFIG)', JSON.stringify(cfg, null, 2)); \
		console.log('Zed configured. Open Zed and pick axeai from the agent dropdown.'); \
	" 2>/dev/null || python3 -c " \
		import json, os; \
		path = os.path.expanduser('$(ZED_CONFIG)'); \
		cfg = json.load(open(path)) if os.path.exists(path) else {}; \
		cfg.setdefault('agent_servers', {})['axeai'] = { \
			'command': '$(INSTALL_DIR)/$(BINARY)', \
			'args': ['--profile', '$(AWS_PROFILE)', '--region', '$(AWS_REGION)', '--model', '$(MODEL)'] \
		}; \
		json.dump(cfg, open(path, 'w'), indent=2); \
		print('Zed configured. Open Zed and pick axeai from the agent dropdown.'); \
	"

clean:
	rm -f $(BINARY)
	cd $(ZIG_DIR) && rm -rf zig-out .zig-cache

test:
	go test ./...

help:
	@echo "targets:"
	@echo "  make              build zig + go binaries"
	@echo "  make install      build and install to $(INSTALL_DIR)"
	@echo "  make zed          build, install, and configure Zed"
	@echo "  make clean        remove build artifacts"
	@echo "  make test         run go tests"
	@echo ""
	@echo "options (pass as make VAR=value):"
	@echo "  AWS_PROFILE       AWS profile name     (default: default)"
	@echo "  AWS_REGION        AWS region           (default: us-east-1)"
	@echo "  MODEL             Bedrock model ID     (default: $(MODEL))"
	@echo ""
	@echo "example:"
	@echo "  make zed AWS_PROFILE=myprofile AWS_REGION=us-west-2"
