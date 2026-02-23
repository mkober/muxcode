PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
CONFIGDIR ?= $(HOME)/.config/muxcode
NVIM_PLUGIN_DIR ?= $(HOME)/.local/share/nvim/site/plugin

.PHONY: build test install clean

build:
	@REPO_DIR="$$(pwd)"; BIN_DIR="$$REPO_DIR/bin"; mkdir -p "$$BIN_DIR"; \
	built=0; last_name=""; \
	for moddir in "$$REPO_DIR"/tools/*/; do \
		[ -f "$$moddir/go.mod" ] || continue; \
		last_name="$$(basename "$$moddir")"; \
		(cd "$$moddir" && go build -ldflags="-s -w" -o "$$BIN_DIR/$$last_name" .); \
		codesign --force --sign - "$$BIN_DIR/$$last_name" 2>/dev/null || true; \
		built=$$((built + 1)); \
	done; \
	if [ $$built -eq 1 ]; then \
		echo "Go binary: Built $$built module → bin/$$last_name"; \
	else \
		echo "Go binary: Built $$built modules → bin/"; \
	fi

test:
	./test.sh

install: build
	@install -d $(BINDIR) $(CONFIGDIR)/agents
	@install -m 755 bin/muxcode-agent-bus $(BINDIR)/muxcode-agent-bus
	@install -m 755 muxcode.sh $(BINDIR)/muxcode
	@for f in scripts/muxcode-*.sh; do \
		[ -f "$$f" ] && install -m 755 "$$f" $(BINDIR)/ ; \
	done; true
	@cp -n agents/*.md $(CONFIGDIR)/agents/ 2>/dev/null || true
	@cp -n config/* $(CONFIGDIR)/ 2>/dev/null || true
	@cp -n muxcode.conf.example $(CONFIGDIR)/config 2>/dev/null || true
	@install -d $(NVIM_PLUGIN_DIR)
	@install -m 644 config/muxcode-startscreen.lua $(NVIM_PLUGIN_DIR)/muxcode-startscreen.lua
	@printf 'Installed: binary to %s/, scripts/agents/configs to %s/\n' "$(BINDIR)" "$(CONFIGDIR)" | sed "s|$$HOME|~|g"

clean:
	rm -rf bin/
	rm -f $(NVIM_PLUGIN_DIR)/muxcode-startscreen.lua
