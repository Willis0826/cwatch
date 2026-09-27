# Build and install targets for cwatch. All builds use CGO_ENABLED=0.
#
#   make setup       Install the binary, show the hook changes, install the
#                    hooks after confirmation, then run doctor.
#   make uninstall   Remove the hooks and the binary. Keep the history.
#
# Variables:
#   BINDIR=DIR       Install directory. Default: /opt/homebrew/bin, else
#                    /usr/local/bin, else ~/.local/bin (first writable one).
#   YES=1            Do not ask for confirmation before the hook install.
#   NO_EXCERPTS=1    Do not store prompt or response excerpts.
#   SETTINGS=FILE    Claude Code settings file (default: cwatch default).

VERSION ?= 0.1.0
LDFLAGS := -s -w -X main.version=$(VERSION)

ARCH := $(shell uname -m | sed 's/x86_64/amd64/')
HAVE_GO := $(shell command -v go >/dev/null 2>&1 && echo yes)

# The first writable directory in this list is the default install directory.
BINDIR ?= $(firstword $(foreach d,/opt/homebrew/bin /usr/local/bin,$(shell test -d $(d) -a -w $(d) && echo $(d))) $(HOME)/.local/bin)
INSTALLED := $(BINDIR)/cwatch

SETUP_FLAGS := $(if $(NO_EXCERPTS),--no-excerpts) $(if $(SETTINGS),--settings-file "$(SETTINGS)")
SETTINGS_FLAG := $(if $(SETTINGS),--settings-file "$(SETTINGS)")

.PHONY: build test race vet dist install hooks-check hooks doctor setup uninstall screenshots clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/cwatch ./cmd/cwatch

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

dist:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/cwatch-darwin-arm64 ./cmd/cwatch
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/cwatch-darwin-amd64 ./cmd/cwatch

# Install the binary. Build it when Go is available. Else use the prebuilt
# binary in dist/ for this architecture.
install:
ifeq ($(HAVE_GO),yes)
	@$(MAKE) --no-print-directory build
	@SRC=bin/cwatch; \
	install -d "$(BINDIR)" && install -m 0755 "$$SRC" "$(INSTALLED)" && echo "Installed $$SRC to $(INSTALLED)"
else
	@SRC=dist/cwatch-darwin-$(ARCH); \
	if [ ! -x "$$SRC" ]; then echo "Go is not installed and $$SRC does not exist. Install Go (brew install go), then run make again." >&2; exit 1; fi; \
	echo "Go is not installed. cwatch uses the prebuilt $$SRC."; \
	install -d "$(BINDIR)" && install -m 0755 "$$SRC" "$(INSTALLED)" && echo "Installed $$SRC to $(INSTALLED)"
endif
	@case ":$$PATH:" in *":$(BINDIR):"*) ;; *) echo "Note: $(BINDIR) is not on your PATH. Add it to ~/.zshrc: export PATH=\"$(BINDIR):\$$PATH\"";; esac
	@"$(INSTALLED)" version

# Show the hook changes. This changes no files.
hooks-check:
	@"$(INSTALLED)" setup --dry-run $(SETUP_FLAGS)

# Install the hooks with the installed binary, so the hooks use its path.
hooks:
	@"$(INSTALLED)" setup $(SETUP_FLAGS)

doctor:
	@"$(INSTALLED)" doctor $(SETTINGS_FLAG)

# Do all steps: install, check the hooks, confirm, install the hooks, doctor.
setup: install
	@echo
	@echo "== Hook check (no changes) =="
	@$(MAKE) --no-print-directory hooks-check
	@echo
	@if [ "$(YES)" != "1" ]; then \
		printf "Install these hooks into the Claude Code settings? [y/N] "; \
		read answer; \
		case "$$answer" in [yY]|[yY][eE][sS]) ;; *) echo "Stopped. cwatch made no changes to the settings."; exit 1;; esac; \
	fi
	@echo "== Hook install =="
	@$(MAKE) --no-print-directory hooks
	@echo
	@echo "== Doctor =="
	@$(MAKE) --no-print-directory doctor
	@echo
	@echo "Done. Restart running Claude Code sessions, submit a prompt in each, then run: cwatch"

# Remove the hooks and the binary. Keep the history in ~/.cwatch.
uninstall:
	@# Any cwatch binary can remove the hooks, because uninstall finds the
	@# cwatch hooks at all executable paths.
	@for b in "$(INSTALLED)" bin/cwatch dist/cwatch-darwin-$(ARCH); do \
		if [ -x "$$b" ]; then "$$b" uninstall $(SETTINGS_FLAG) || exit 1; exit 0; fi; \
	done; \
	echo "No cwatch binary found. Run make install, then make uninstall." >&2; exit 1
	@rm -f "$(INSTALLED)" && echo "Removed $(INSTALLED)"
	@echo "cwatch kept the history. Delete ~/.cwatch to remove it."

# Make the README images from demo data. Needs Go, Python 3, and Google Chrome.
screenshots:
	@tmp=$$(mktemp -d) && \
	CWATCH_DEMO_DIR=$$tmp go test -count=1 -run TestRenderDemo ./internal/tui/ >/dev/null && \
	python3 scripts/ansi2png.py $$tmp/dashboard.ans docs/images/dashboard.png "🟡1 🔴1 🟢2 ⚪2 · cwatch" && \
	python3 scripts/ansi2png.py $$tmp/details.ans docs/images/details.png "🟡1 🔴1 🟢2 ⚪2 · cwatch" && \
	rm -rf $$tmp

clean:
	rm -rf bin
