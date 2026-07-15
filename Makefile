BINARY := profiledeck
BIN_DIR := bin
TOOLS_DIR := $(BIN_DIR)/tools
CMD := ./cmd/profiledeck
DESKTOP_CMD := ./desktop
DESKTOP_FRONTEND := desktop/frontend
RELEASE_TOOL_PKGS := ./scripts/feedtool ./scripts/updatee2e/server
UPDATE_E2E_PKG := ./scripts/updatee2e/client
DOCS_DIR := docs
CORE_PKGS := ./cmd/... ./internal/...
DESKTOP_PKGS := ./desktop/...
GO_PKGS := $(CORE_PKGS) $(DESKTOP_PKGS)
GOLANGCI_LINT ?= golangci-lint
WAILS3 ?= wails3
GOLANGCI_LINT_VERSION := v2.12.2
WAILS3_VERSION := v3.0.0-alpha2.115
CI_GOLANGCI_LINT_DIR := $(TOOLS_DIR)/golangci-lint/$(GOLANGCI_LINT_VERSION)
CI_GOLANGCI_LINT := $(CI_GOLANGCI_LINT_DIR)/golangci-lint
CI_WAILS3_DIR := $(TOOLS_DIR)/wails3/$(WAILS3_VERSION)
CI_WAILS3 := $(CI_WAILS3_DIR)/wails3
DESKTOP_GOOS ?= $(or $(GOOS),$(shell go env GOOS))
DESKTOP_GOARCH ?= $(or $(GOARCH),$(shell go env GOARCH))
MACOS_MIN_VERSION ?= 14.0
CGO_CFLAGS ?= -O2 -g
CGO_CXXFLAGS ?= -O2 -g
CGO_LDFLAGS ?= -O2 -g
DESKTOP_GO_ENV := GOOS=$(DESKTOP_GOOS) GOARCH=$(DESKTOP_GOARCH)
DESKTOP_VERSION ?=
DESKTOP_BUILD_NUMBER ?=
UPDATE_PUBLIC_KEY_BASE64 ?=
LOCAL_DESKTOP_VERSION ?= 0.1.0-alpha.0.local
LOCAL_DESKTOP_BUILD_NUMBER ?= $(shell git rev-list --count HEAD)
DIST_DIR ?= $(CURDIR)/dist
ifeq ($(DESKTOP_GOOS),darwin)
DESKTOP_GO_ENV += MACOSX_DEPLOYMENT_TARGET=$(MACOS_MIN_VERSION)
DESKTOP_GO_ENV += CGO_CFLAGS="$(CGO_CFLAGS) -mmacosx-version-min=$(MACOS_MIN_VERSION)"
DESKTOP_GO_ENV += CGO_CXXFLAGS="$(CGO_CXXFLAGS) -mmacosx-version-min=$(MACOS_MIN_VERSION)"
DESKTOP_GO_ENV += CGO_LDFLAGS="$(CGO_LDFLAGS) -mmacosx-version-min=$(MACOS_MIN_VERSION)"
endif

.PHONY: fmt vet lint lint-core lint-desktop lint-release-tools test build core-boundary core-check check clean wails-boundary desktop-go-fmt desktop-bindings desktop-bindings-check desktop-taskfile-check desktop-frontend-install desktop-frontend-check desktop-frontend-build desktop-build desktop-package desktop-package-local release-tools-check verify-update-e2e desktop-check docs-install docs-dev docs-build docs-preview docs-check ci-core-check ci-desktop-check

fmt:
	$(GOLANGCI_LINT) fmt $(GO_PKGS)
	$(GOLANGCI_LINT) fmt ./scripts/...

vet: lint-core

lint: lint-core lint-desktop lint-release-tools

lint-core:
	$(GOLANGCI_LINT) run $(CORE_PKGS)

lint-desktop:
	$(DESKTOP_GO_ENV) $(GOLANGCI_LINT) run $(DESKTOP_PKGS)

lint-release-tools:
	$(DESKTOP_GO_ENV) $(GOLANGCI_LINT) run $(RELEASE_TOOL_PKGS)
	$(DESKTOP_GO_ENV) $(GOLANGCI_LINT) run --build-tags updatee2e $(UPDATE_E2E_PKG)

test:
	go test $(CORE_PKGS)

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY) $(CMD)

core-boundary:
	go test ./internal/architecture

core-check: lint-core core-boundary test build

check: core-check desktop-check docs-check

wails-boundary:
	! rg -n 'github.com/wailsapp/wails|@wailsio/runtime' cmd internal

desktop-go-fmt:
	$(GOLANGCI_LINT) fmt $(DESKTOP_PKGS)

desktop-bindings:
	$(WAILS3) generate bindings -d $(DESKTOP_FRONTEND)/bindings -ts -i $(DESKTOP_PKGS)

desktop-bindings-check:
	@temp_dir=$$(mktemp -d); \
	trap 'rm -rf "$$temp_dir"' EXIT; \
	$(WAILS3) generate bindings -d "$$temp_dir" -ts -i $(DESKTOP_PKGS); \
	diff -ru $(DESKTOP_FRONTEND)/bindings "$$temp_dir"

desktop-taskfile-check:
	$(WAILS3) task --list >/dev/null
	$(WAILS3) task build GOOS=darwin -dry >/dev/null
	$(WAILS3) task build GOOS=darwin DEV=true EXTRA_TAGS=taskfilecheck -dry >/dev/null
	$(WAILS3) task build GOOS=windows DEV=true EXTRA_TAGS=taskfilecheck -dry >/dev/null
	$(WAILS3) task build GOOS=linux DEV=true EXTRA_TAGS=taskfilecheck -dry >/dev/null

desktop-frontend-install:
	npm --prefix $(DESKTOP_FRONTEND) ci

desktop-frontend-check: desktop-frontend-install
	npm --prefix $(DESKTOP_FRONTEND) run check
	npm --prefix $(DESKTOP_FRONTEND) run test:unit

desktop-frontend-build: desktop-frontend-install
	npm --prefix $(DESKTOP_FRONTEND) run build

desktop-build: desktop-frontend-build
	mkdir -p $(BIN_DIR)
	$(DESKTOP_GO_ENV) go build -tags production -o $(BIN_DIR)/profiledeck-desktop $(DESKTOP_CMD)

desktop-package: desktop-frontend-build
	PROFILEDECK_VERSION="$(DESKTOP_VERSION)" \
	PROFILEDECK_BUILD_NUMBER="$(DESKTOP_BUILD_NUMBER)" \
	PROFILEDECK_UPDATE_PUBLIC_KEY_BASE64="$(UPDATE_PUBLIC_KEY_BASE64)" \
	PROFILEDECK_DIST_DIR="$(DIST_DIR)" \
	./scripts/package-macos.sh

desktop-package-local: $(CI_WAILS3)
	@temp_dir=$$(mktemp -d); \
	trap 'rm -rf "$$temp_dir"' EXIT; \
	key_path="$$temp_dir/update.key"; \
	$(CI_WAILS3) updater genkey -out "$$key_path" >/dev/null; \
	public_key=$$(go run ./scripts/feedtool public-key --private-key "$$key_path"); \
	$(MAKE) desktop-package \
		DESKTOP_VERSION="$(LOCAL_DESKTOP_VERSION)" \
		DESKTOP_BUILD_NUMBER="$(LOCAL_DESKTOP_BUILD_NUMBER)" \
		UPDATE_PUBLIC_KEY_BASE64="$$public_key"

verify-update-e2e:
	./scripts/test-update-restart.sh

release-tools-check: lint-release-tools
	$(DESKTOP_GO_ENV) go test $(RELEASE_TOOL_PKGS)
	$(DESKTOP_GO_ENV) go test -tags updatee2e $(UPDATE_E2E_PKG)
	bash -n scripts/package-macos.sh scripts/verify-macos-artifact.sh scripts/test-update-restart.sh

desktop-check: wails-boundary lint-desktop release-tools-check desktop-bindings-check desktop-taskfile-check desktop-frontend-check desktop-build
	$(DESKTOP_GO_ENV) go test ./desktop/...

docs-install:
	npm --prefix $(DOCS_DIR) ci

docs-dev: docs-install
	npm --prefix $(DOCS_DIR) run dev

docs-build: docs-install
	npm --prefix $(DOCS_DIR) run build

docs-preview: docs-build
	npm --prefix $(DOCS_DIR) run preview

docs-check: docs-build

$(CI_GOLANGCI_LINT):
	mkdir -p $(CI_GOLANGCI_LINT_DIR)
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(CI_GOLANGCI_LINT_DIR) $(GOLANGCI_LINT_VERSION)

$(CI_WAILS3):
	mkdir -p $(CI_WAILS3_DIR)
	GOBIN=$(abspath $(CI_WAILS3_DIR)) go install github.com/wailsapp/wails/v3/cmd/wails3@$(WAILS3_VERSION)

ci-core-check: $(CI_GOLANGCI_LINT)
	$(MAKE) core-check GOLANGCI_LINT=$(abspath $(CI_GOLANGCI_LINT))

ci-desktop-check: $(CI_GOLANGCI_LINT) $(CI_WAILS3)
	$(MAKE) desktop-check GOLANGCI_LINT=$(abspath $(CI_GOLANGCI_LINT)) WAILS3=$(abspath $(CI_WAILS3))

clean:
	rm -rf $(BIN_DIR)
