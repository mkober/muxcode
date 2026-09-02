PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
CONFIGDIR ?= $(HOME)/.config/muxcode
NVIM_CONFIGDIR ?= $(HOME)/.config/muxcode/nvim
NVIM_PLUGIN_DIR ?= $(HOME)/.local/share/nvim/site/plugin

# Build identity stamped into both binaries (tools/muxcode/bus/version.go,
# tools/muxcode-llm-harness/harness/version.go). `git describe` yields the
# tag, or the tag plus distance and commit past it, with -dirty for a modified
# tree; a build outside a checkout reads "devel". One flag set serves both
# modules because the linker ignores a -X target that is not linked in.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUS_MODULE := github.com/mkober/muxcode/tools/muxcode
HARNESS_MODULE := muxcode-llm-harness
LDFLAGS := -s -w \
	-X $(BUS_MODULE)/bus.Version=$(VERSION) -X $(BUS_MODULE)/bus.Commit=$(COMMIT) -X $(BUS_MODULE)/bus.BuildDate=$(DATE) \
	-X $(HARNESS_MODULE)/harness.Version=$(VERSION) -X $(HARNESS_MODULE)/harness.Commit=$(COMMIT) -X $(HARNESS_MODULE)/harness.BuildDate=$(DATE)

.PHONY: build test install clean

# The whole recipe is one shell command, so without `set -e` a failing
# `go build` inside the loop was ignored: the recipe's exit status came from
# the trailing `if/echo`, which always succeeds. Make then reported success,
# `install` ran on its stale prerequisite, and a broken build silently
# installed the previous binary while printing "Built N modules".
build:
	@set -e; \
	REPO_DIR="$$(pwd)"; BIN_DIR="$$REPO_DIR/bin"; mkdir -p "$$BIN_DIR"; \
	built=0; last_name=""; \
	for moddir in "$$REPO_DIR"/tools/*/; do \
		[ -f "$$moddir/go.mod" ] || continue; \
		name="$$(basename "$$moddir")"; \
		if ! (cd "$$moddir" && go build -ldflags="$(LDFLAGS)" -o "$$BIN_DIR/$$name" .); then \
			echo "Go build FAILED for module $$name — not installing" >&2; \
			exit 1; \
		fi; \
		if [ ! -x "$$BIN_DIR/$$name" ]; then \
			echo "Go build produced no executable for module $$name" >&2; \
			exit 1; \
		fi; \
		last_name="$$name"; \
		codesign --force --sign - "$$BIN_DIR/$$name" 2>/dev/null || true; \
		built=$$((built + 1)); \
	done; \
	if [ $$built -eq 0 ]; then \
		echo "No Go modules found under tools/ — nothing built" >&2; \
		exit 1; \
	fi; \
	if [ $$built -eq 1 ]; then \
		echo "Go binary: Built $$built module → bin/$$last_name ($(VERSION))"; \
	else \
		echo "Go binary: Built $$built modules → bin/ ($(VERSION))"; \
	fi

test:
	./test.sh

install: build
	@# Defense in depth: `build` already exits non-zero on a compile failure,
	@# but `make install` can be reached with a stale or absent bin/. Refuse
	@# rather than install something that was never successfully built.
	@[ -x bin/muxcode ] || { echo "bin/muxcode missing or not executable — refusing to install" >&2; exit 1; }
	@install -d $(BINDIR) $(CONFIGDIR)/agents
	@install -m 755 bin/muxcode $(BINDIR)/muxcode
	@ln -sf muxcode $(BINDIR)/muxcode-agent-bus
	@[ -f bin/muxcode-llm-harness ] && install -m 755 bin/muxcode-llm-harness $(BINDIR)/muxcode-llm-harness || true
	@# Install hook and utility scripts (muxcode-agent.sh removed — agent
	@# launch is now handled natively by the Go binary via "muxcode agent launch")
	@for f in scripts/muxcode-*.sh; do \
		[ -f "$$f" ] && install -m 755 "$$f" $(BINDIR)/ ; \
	done; true
	@# Clean up legacy scripts replaced by native Go (safe to run on fresh installs)
	@rm -f $(BINDIR)/muxcode-agent.sh $(BINDIR)/muxcode.sh
	@# Agents and skills always overwrite — these are repo defaults that track
	@# upstream changes. User customizations go in .claude/agents/ (project-level
	@# 3-tier resolution) or .muxcode/skills/, not in ~/.config/muxcode/.
	@cp agents/*.md $(CONFIGDIR)/agents/ 2>/dev/null || true
	@install -d $(CONFIGDIR)/skills
	@cp skills/*.md $(CONFIGDIR)/skills/ 2>/dev/null || true
	@# Config files always overwrite — these track upstream changes.
	@# User customizations go in .muxcode/ (project-level, higher priority).
	@cp config/muxcode.json $(CONFIGDIR)/muxcode.json
	@cp config/settings.json $(CONFIGDIR)/settings.json
	@cp -n config/plugins.conf $(CONFIGDIR)/plugins.conf 2>/dev/null || true
	@# models.conf overwrites (no -n, unlike plugins.conf above): it is the
	@# provider-selector model list, so a model rename upstream must reach the
	@# installed copy. With -n every rename silently stopped at the source.
	@cp config/models.conf $(CONFIGDIR)/models.conf
	@cp config/tmux.conf $(CONFIGDIR)/tmux.conf
	@install -d $(HOME)/.claude/commands
	@cp -n config/commands/*.md $(HOME)/.claude/commands/ 2>/dev/null || true
	@cp -n muxcode.conf.example $(CONFIGDIR)/config 2>/dev/null || true
	@install -d $(NVIM_CONFIGDIR)/plugin
	@install -m 644 config/nvim/init.lua $(NVIM_CONFIGDIR)/init.lua
	@install -m 644 config/nvim/plugin/startscreen.lua $(NVIM_CONFIGDIR)/plugin/startscreen.lua
	@# Clean up old site plugin from pre-NVIM_APPNAME installs
	@rm -f $(NVIM_PLUGIN_DIR)/muxcode-startscreen.lua
	@printf 'Installed: binary to %s/, agents/configs to %s/\n' "$(BINDIR)" "$(CONFIGDIR)" | sed "s|$$HOME|~|g"

clean:
	rm -rf bin/
	rm -f $(NVIM_PLUGIN_DIR)/muxcode-startscreen.lua
	rm -rf $(NVIM_CONFIGDIR)
