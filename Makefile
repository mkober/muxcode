PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
CONFIGDIR ?= $(HOME)/.config/muxcode

.PHONY: build test install clean

build:
	@REPO_DIR="$$(pwd)"; BIN_DIR="$$REPO_DIR/bin"; mkdir -p "$$BIN_DIR"; \
	built=0; \
	for moddir in "$$REPO_DIR"/tools/*/; do \
		[ -f "$$moddir/go.mod" ] || continue; \
		name="$$(basename "$$moddir")"; \
		echo "Building $$name..."; \
		(cd "$$moddir" && go build -ldflags="-s -w" -o "$$BIN_DIR/$$name" .); \
		codesign --force --sign - "$$BIN_DIR/$$name" 2>/dev/null || true; \
		built=$$((built + 1)); \
	done; \
	echo "Built $$built module(s) → $$BIN_DIR/"

test:
	./test.sh

install: build
	install -d $(BINDIR) $(CONFIGDIR)/agents
	install -m 755 bin/muxcode-agent-bus $(BINDIR)/muxcode-agent-bus
	install -m 755 muxcode.sh $(BINDIR)/muxcode
	@for f in scripts/muxcode-*.sh; do \
		[ -f "$$f" ] && install -m 755 "$$f" $(BINDIR)/ ; \
	done; true
	cp -n agents/*.md $(CONFIGDIR)/agents/ 2>/dev/null || true
	cp -n config/* $(CONFIGDIR)/ 2>/dev/null || true
	cp -n muxcode.conf.example $(CONFIGDIR)/config 2>/dev/null || true

clean:
	rm -rf bin/
