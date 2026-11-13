# Uses mise's Go: /usr/bin/go is mismatched with the GOROOT mise exports.
GO ?= $(HOME)/.local/share/mise/installs/go/1.27.3/bin/go

ccdesk: *.go go.mod
	$(GO) build -ldflags="-s -w -X 'gioui.org/app.ID=ccdesk'" -o ccdesk .

install: ccdesk
	install -Dm755 ccdesk $(HOME)/.local/bin/ccdesk
	install -Dm644 ccdesk.desktop $(HOME)/.local/share/applications/ccdesk.desktop
	install -Dm644 assets/ccdesk.svg $(HOME)/.local/share/icons/hicolor/scalable/apps/ccdesk.svg

.PHONY: install
