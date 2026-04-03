.PHONY: generate build clean restart skill
.DEFAULT_GOAL := restart

BINARY := bin/spirit
VERSION ?= dev

generate:
	go generate ./internal/scripting/

build: generate
	@mkdir -p bin
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/spirit

restart: build
	@$(BINARY) daemon --stop 2>/dev/null || true
	@$(BINARY) daemon &
	@echo "daemon restarted"

skill: build
	@mkdir -p ~/.claude/skills/spirit
	@$(BINARY) _gen-skill > ~/.claude/skills/spirit/SKILL.md
	@mkdir -p ~/.cache/spirit/copilot-workspace/skills/spirit
	@$(BINARY) _gen-skill > ~/.cache/spirit/copilot-workspace/skills/spirit/SKILL.md
	@echo "SKILL.md installed (claude-code + openclaw)"

clean:
	rm -rf bin
