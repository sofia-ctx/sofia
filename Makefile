.PHONY: help build test tidy fmt vet check clean install uninstall completions update

# Where `make install` links the sf binary; must be on $PATH.
BINDIR ?= $(HOME)/.local/bin

# Claude Code home — `make install` drops the sf-context skill here.
CLAUDE_DIR ?= $(HOME)/.claude

# Running `make` with no target prints the help below.
.DEFAULT_GOAL := help

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN{FS=":.*## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

# Build every cmd/** binary into bin/**, preserving the common/ vs
# projects/<name>/ layout.
build: ## Build all binaries into bin/**
	./scripts/build.sh

test: ## Run all Go tests
	go test ./...

tidy: ## go mod tidy
	go mod tidy

fmt: ## gofmt the tree
	gofmt -w .

vet: ## go vet ./...
	go vet ./...

# Pre-commit gate: vet + tests.
check: vet test ## Pre-commit gate (vet + test)

# Build and symlink `sf` into BINDIR so it's on $PATH (no shell aliases),
# and refresh shell completions. `sf claude <project>` then launches Claude
# Code for a project.
install: build completions ## Build + symlink sf into BINDIR + completions + sf-context skill
	mkdir -p "$(BINDIR)"
	ln -sf "$(CURDIR)/bin/sf" "$(BINDIR)/sf"
	@echo "linked $(BINDIR)/sf -> $(CURDIR)/bin/sf"
	mkdir -p "$(CLAUDE_DIR)/skills/sf-context"
	cp -f skills/sf-context/SKILL.md "$(CLAUDE_DIR)/skills/sf-context/SKILL.md"
	@echo "installed skill sf-context -> $(CLAUDE_DIR)/skills/sf-context/"

# Regenerate bash/fish completions from the freshly built binary so they
# always track the current command tree. Runs on every `make install`.
completions: build ## Regenerate bash/fish shell completions
	@if command -v fish >/dev/null 2>&1; then \
		mkdir -p "$(HOME)/.config/fish/completions"; \
		"$(CURDIR)/bin/sf" completion fish > "$(HOME)/.config/fish/completions/sf.fish" && \
		echo "fish completion -> ~/.config/fish/completions/sf.fish"; \
	fi
	@mkdir -p "$(HOME)/.local/share/bash-completion/completions"; \
	"$(CURDIR)/bin/sf" completion bash > "$(HOME)/.local/share/bash-completion/completions/sf" && \
	echo "bash completion -> ~/.local/share/bash-completion/completions/sf"

# Self-update: pull the latest main, rebuild every binary, and regenerate
# shell completions in one shot (à la `rustup update`). The install symlink
# keeps working because bin/sf is rebuilt in place — run `make install` once
# first if you've never linked it. `completions` already depends on `build`,
# so this builds exactly once.
update: ## Self-update: git pull + rebuild + refresh completions
	git pull --ff-only
	$(MAKE) completions

uninstall: ## Remove the sf symlink, completions and the sf-context skill
	rm -f "$(BINDIR)/sf" \
		"$(HOME)/.config/fish/completions/sf.fish" \
		"$(HOME)/.local/share/bash-completion/completions/sf"
	rm -rf "$(CLAUDE_DIR)/skills/sf-context"

clean: ## Remove bin/
	rm -rf bin/
