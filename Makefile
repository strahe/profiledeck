BINARY := profiledeck
BIN_DIR := bin
CMD := ./cmd/profiledeck
DESKTOP_CMD := ./desktop
DESKTOP_FRONTEND := desktop/frontend
PKGS := ./cmd/... ./internal/...
DESKTOP_GOOS ?= $(or $(GOOS),$(shell go env GOOS))
DESKTOP_GOARCH ?= $(or $(GOARCH),$(shell go env GOARCH))
MACOS_MIN_VERSION ?= 14.0
CGO_CFLAGS ?= -O2 -g
CGO_CXXFLAGS ?= -O2 -g
CGO_LDFLAGS ?= -O2 -g
DESKTOP_GO_ENV := GOOS=$(DESKTOP_GOOS) GOARCH=$(DESKTOP_GOARCH)
ifeq ($(DESKTOP_GOOS),darwin)
DESKTOP_GO_ENV += MACOSX_DEPLOYMENT_TARGET=$(MACOS_MIN_VERSION)
DESKTOP_GO_ENV += CGO_CFLAGS="$(CGO_CFLAGS) -mmacosx-version-min=$(MACOS_MIN_VERSION)"
DESKTOP_GO_ENV += CGO_CXXFLAGS="$(CGO_CXXFLAGS) -mmacosx-version-min=$(MACOS_MIN_VERSION)"
DESKTOP_GO_ENV += CGO_LDFLAGS="$(CGO_LDFLAGS) -mmacosx-version-min=$(MACOS_MIN_VERSION)"
endif

.PHONY: fmt vet test build check clean wails-boundary desktop-go-fmt desktop-bindings desktop-frontend-install desktop-frontend-check desktop-frontend-build desktop-build desktop-check

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

wails-boundary:
	! rg -n 'github.com/wailsapp/wails|@wailsio/runtime' cmd internal

desktop-go-fmt:
	go fmt ./desktop/...

desktop-bindings:
	wails3 generate bindings -d $(DESKTOP_FRONTEND)/bindings -ts -i ./desktop/...

desktop-frontend-install:
	npm --prefix $(DESKTOP_FRONTEND) ci

desktop-frontend-check: desktop-frontend-install desktop-bindings
	npm --prefix $(DESKTOP_FRONTEND) run check

desktop-frontend-build: desktop-frontend-install desktop-bindings
	npm --prefix $(DESKTOP_FRONTEND) run build

desktop-build: desktop-frontend-build
	mkdir -p $(BIN_DIR)
	$(DESKTOP_GO_ENV) go build -tags production -o $(BIN_DIR)/profiledeck-desktop $(DESKTOP_CMD)

desktop-check: wails-boundary desktop-go-fmt desktop-frontend-check desktop-build
	$(DESKTOP_GO_ENV) go test ./desktop/...

clean:
	rm -rf $(BIN_DIR)
