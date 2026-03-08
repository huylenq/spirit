.PHONY: build clean restart
.DEFAULT_GOAL := restart

BINARY := bin/cmc
VERSION ?= dev

build:
	@mkdir -p bin
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/cmc

restart: build
	@$(BINARY) daemon --stop 2>/dev/null || true
	@$(BINARY) daemon &
	@echo "daemon restarted"

clean:
	rm -rf bin
