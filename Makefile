BIN := bin
# Release builds name their version; others report the git commit.
VERSION ?=
LDFLAGS := $(if $(VERSION),-ldflags "-X main.version=$(VERSION)")
AGENT := ./cmd/haobot-agent
LABEL := com.haobot.agent

.PHONY: build linux dist test vet clean install-launchd ui ui-dev

# Builds the admin UI (ui/) into internal/agent/web/dist, which the agent
# embeds. The built files are kept in the repository, so Go builds do not
# need Node; run this after changing ui/.
ui:
	cd ui && pnpm install --frozen-lockfile && pnpm build

# UI dev server with hot reload; proxies /api to an agent on 127.0.0.1:9081 (or $$HAOBOT_PORT).
ui-dev:
	cd ui && pnpm dev

# Native build. On macOS this includes the libusb transport (brew install libusb pkg-config).
build:
	go build $(LDFLAGS) -o $(BIN)/haobot-agent $(AGENT)

# Static Linux binaries without cgo; they use the serial transport (/dev/ttyUSB*).
linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BIN)/haobot-agent-linux-amd64 $(AGENT)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BIN)/haobot-agent-linux-arm64 $(AGENT)

# Release archives in dist/: the Linux ones, plus macOS for this Mac's
# architecture. Tagging v* on GitHub builds every platform (release.yml).
dist:
	@test -n "$(VERSION)" || { echo "用法: make dist VERSION=v1.2.3"; exit 1; }
	VERSION=$(VERSION) GOOS=linux GOARCH=amd64 scripts/dist.sh
	VERSION=$(VERSION) GOOS=linux GOARCH=arm64 scripts/dist.sh
	VERSION=$(VERSION) GOOS=linux GOARCH=arm GOARM=7 scripts/dist.sh
	if [ "$$(go env GOHOSTOS)" = darwin ]; then VERSION=$(VERSION) GOOS=darwin scripts/dist.sh; fi

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf $(BIN) dist

# Installs the agent to ~/.local/bin and a per-user LaunchAgent that starts at login.
install-launchd: build
	mkdir -p $(HOME)/.local/bin $(HOME)/Library/LaunchAgents $(HOME)/Library/Logs
	cp $(BIN)/haobot-agent $(HOME)/.local/bin/haobot-agent
	sed -e 's#__BIN__#$(HOME)/.local/bin/haobot-agent#' -e 's#__HOME__#$(HOME)#g' \
		deploy/agent/$(LABEL).plist > $(HOME)/Library/LaunchAgents/$(LABEL).plist
	launchctl bootout gui/$$(id -u)/$(LABEL) 2>/dev/null || true
	launchctl bootstrap gui/$$(id -u) $(HOME)/Library/LaunchAgents/$(LABEL).plist
	@echo "已安装。日志：$(HOME)/Library/Logs/haobot-agent.log"
