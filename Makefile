BINARY := profiledeck
BIN_DIR := bin
CMD := ./cmd/profiledeck
PKGS := ./...

.PHONY: fmt vet test build check clean

fmt:
	go fmt $(PKGS)

vet:
	go vet $(PKGS)

test:
	go test $(PKGS)

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY) $(CMD)

check: fmt vet test build

clean:
	rm -rf $(BIN_DIR)
