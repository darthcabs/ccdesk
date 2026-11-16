# ccdesk — native Claude Code desktop client
GO      ?= go
PREFIX  ?= $(HOME)/.local
BIN     := ccdesk
SOURCES := $(wildcard *.go) go.mod go.sum $(shell find third_party -name '*.go' -o -name '*.c' -o -name '*.h')

$(BIN): $(SOURCES)
	$(GO) build -trimpath -ldflags="-s -w -X 'gioui.org/app.ID=ccdesk'" -o $(BIN) .

## install: build and install ccdesk, its launcher entry and icon under PREFIX
install: $(BIN)
	install -Dm755 $(BIN) $(PREFIX)/bin/ccdesk
	install -Dm644 ccdesk.desktop $(PREFIX)/share/applications/ccdesk.desktop
	install -Dm644 assets/ccdesk.svg $(PREFIX)/share/icons/hicolor/scalable/apps/ccdesk.svg
	-update-desktop-database $(PREFIX)/share/applications 2>/dev/null
	-gtk-update-icon-cache -q $(PREFIX)/share/icons/hicolor 2>/dev/null

## connector: download and verify the claude-agent-acp connector
connector:
	./scripts/install-connector.sh

## uninstall: remove what `make install` installed (not your config or Claude data)
uninstall:
	rm -f $(PREFIX)/bin/ccdesk $(PREFIX)/share/applications/ccdesk.desktop \
	      $(PREFIX)/share/icons/hicolor/scalable/apps/ccdesk.svg

## test: run the unit tests
test:
	$(GO) test ./...

## run: build and start ccdesk from the source tree
run: $(BIN)
	./$(BIN)

clean:
	rm -f $(BIN)

help:
	@grep -E '^## ' Makefile | sed 's/^## /  make /'

.PHONY: install connector uninstall test run clean help
