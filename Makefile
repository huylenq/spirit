.PHONY: generate build clean restart skill
.DEFAULT_GOAL := restart

BINARY := bin/cmc
VERSION ?= dev

generate:
	go generate ./internal/scripting/

build: generate
	@mkdir -p bin
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/cmc

restart: build
	@$(BINARY) daemon --stop 2>/dev/null || true
	@$(BINARY) daemon &
	@echo "daemon restarted"

skill: build
	@mkdir -p ~/.claude/skills/cmc
	@$(BINARY) _gen-skill > ~/.claude/skills/cmc/SKILL.md
	@mkdir -p ~/.cache/cmc/copilot-workspace/skills/cmc
	@$(BINARY) _gen-skill > ~/.cache/cmc/copilot-workspace/skills/cmc/SKILL.md
	@echo "SKILL.md installed (claude-code + openclaw)"

clean:
	rm -rf bin
