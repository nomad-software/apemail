BINARY := apemail

# -s -w leaves out the symbol table and debug information, which roughly
# halves the size of the binary.
LDFLAGS := -s -w

# The version the about box shows: the tag the commit is, or follows. Go
# records none that would do, as it ignores tags from v2 on for a module
# path without /v2.
VERSION := $(shell git describe --tags --abbrev=0 2>/dev/null)
LDFLAGS += -X github.com/nomad-software/apemail/internal/ui.version=$(VERSION)

.PHONY: build run test bench vet screenshot appimage clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

run: build
	./$(BINARY)

# Every run of the tests has a timeout, so that one that hangs fails with
# the stacks of what it was waiting on, rather than waiting for ever.
TEST_TIMEOUT := 5m

test:
	go test -race -timeout $(TEST_TIMEOUT) ./...

# Measures how a folder of 100,000 messages fares: saving and reading its
# index, and the message list sorting it and showing a change to it.
bench:
	go test -timeout $(TEST_TIMEOUT) -run '^$$' -bench 100k ./internal/store ./internal/ui

vet:
	go vet ./...

# Draws the window, with a sample mailbox from a server in memory, into
# docs/screenshot.png.
screenshot:
	mkdir -p docs
	APEMAIL_SCREENSHOT=$(CURDIR)/docs/screenshot.png go test -count=1 -timeout $(TEST_TIMEOUT) -run '^TestScreenshot$$' ./internal/ui/

# Packs the program and the Qt libraries it uses into one file that runs
# without installing anything. Releases are made by the GitHub workflow, on
# an old distribution so that they run on more systems.
appimage:
	packaging/appimage.sh

clean:
	rm -f $(BINARY) *.AppImage
