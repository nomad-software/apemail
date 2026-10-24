BINARY := apemail

# -s -w leaves out the symbol table and debug information, which roughly
# halves the size of the binary.
LDFLAGS := -s -w

.PHONY: build run test bench vet screenshot icons appimage macos clean

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

# Draws the icon of the macOS app, packaging/macos/apemail.png, from the
# program's icon, within the margin Apple's grid gives icons. Run it again
# whenever the icon changes; a test says when it has not been.
icons:
	APEMAIL_ICONS=$(CURDIR)/packaging go test -count=1 -timeout $(TEST_TIMEOUT) -run '^TestWriteIcons$$' ./internal/ui/

# Packs the program and the Qt libraries it uses into one file that runs
# without installing anything. Releases are made by the GitHub workflow, on
# an old distribution so that they run on more systems.
appimage:
	packaging/appimage.sh

# Builds the macOS app for Apple Silicon, with Qt inside it, in a disk image.
# It runs on a Mac with Homebrew's Qt; releases are made by the GitHub
# workflow.
macos:
	packaging/macos.sh

clean:
	rm -f $(BINARY)
	rm -rf build
