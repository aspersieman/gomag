BINARY  := gomag
PREFIX  ?= $(HOME)/.local
BINDIR  ?= $(PREFIX)/bin
# Extra flags for `make dev`, e.g. make dev ARGS="-radius 150 -blur"
ARGS    ?= -live -verbose

.PHONY: help build dev run test vet install uninstall clean

help:
	@echo "make build      build ./bin/$(BINARY)"
	@echo "make dev        build and run with ARGS='$(ARGS)' (Ctrl-C to quit)"
	@echo "make test       vet and run tests"
	@echo "make install    install to $(BINDIR) (override with PREFIX=/usr/local)"
	@echo "make uninstall  remove the installed binary"
	@echo "make clean      remove build output"

build:
	go build -trimpath -ldflags "-s -w" -o bin/$(BINARY) ./cmd/gomag

dev run: build
	./bin/$(BINARY) $(ARGS)

vet:
	go vet ./...

test: vet
	go test ./...

install: build
	install -Dm755 bin/$(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)

clean:
	rm -rf bin
