BINARY := apemail

# -s -w leaves out the symbol table and debug information, which roughly
# halves the size of the binary.
LDFLAGS := -s -w

.PHONY: build run test bench vet screenshot clean

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

clean:
	rm -f $(BINARY)
