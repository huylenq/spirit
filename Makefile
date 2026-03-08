.PHONY: build clean

BINARY := bin/cmc
VERSION ?= dev

build:
	@mkdir -p bin
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/cmc

clean:
	rm -rf bin
