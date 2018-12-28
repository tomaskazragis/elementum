PROJECT = elgatito
NAME = elementum
GO_PKG = github.com/elgatito/elementum
XGO_LOCAL =
XGO_VERSION = 1.11.4
GO = go
GIT = git
GIT_VERSION = $(shell $(GIT) describe --tags)
GOTORRENT_VERSION = $(shell (cd $(GOPATH)/src/github.com/anacrolix/torrent; $(GIT) rev-parse HEAD))
OUTPUT_NAME = $(NAME)$(EXT)
BUILD_PATH = build/
GO_BUILD_TAGS =
GO_LDFLAGS = -w -X $(GO_PKG)/util.Version=$(GIT_VERSION) -X $(GO_PKG)/util.GoTorrentVersion=$(GOTORRENT_VERSION)
GO_EXTRALDFLAGS =
PLATFORMS = \
	android-16/arm \
	android-16/386 \
	android-21/arm64 \
	darwin-10.6/amd64 \
	darwin-10.6/386 \
	linux/arm-6 \
	linux/arm-7 \
	linux/arm64 \
	linux/amd64 \
	linux/386 \
	windows-6.0/amd64 \
	windows-6.0/386

.PHONY: $(PLATFORMS)

all:
	for i in $(PLATFORMS); do \
		$(MAKE) $$i; \
	done

client: 
	mkdir -p $(BUILD_PATH)/client

$(PLATFORMS):
	$(MAKE) build TARGET_OS=$(firstword $(subst /, ,$@)) TARGET_ARCH=$(word 2, $(subst /, ,$@))

force:
	@true

$(BUILD_PATH):
	mkdir -p $(BUILD_PATH)

# $(BUILD_PATH)/$(OUTPUT_NAME): $(BUILD_PATH) force
# 	LDFLAGS='$(LDFLAGS)' \
# 	CC='$(CC)' CXX='$(CXX)' \
# 	GOOS='$(GOOS)' GOARCH='$(GOARCH)' GOARM='$(GOARM)' \
# 	CGO_ENABLED='$(CGO_ENABLED)' \
# 	$(GO) build -v $(GO_BUILD_TAGS) \
# 		-gcflags '$(GO_GCFLAGS)' \
# 		-ldflags '$(GO_LDFLAGS)' \
# 		-o '$(BUILD_PATH)/$(OUTPUT_NAME)' \
# 		$(PKGDIR) && \
# 	set -x && \
# 	$(GO) tool vet -unsafeptr=false .

elementum: $(BUILD_PATH)/$(OUTPUT_NAME)

re: clean build

clean:
	rm -rf $(BUILD_PATH)

distclean:
	rm -rf build

build: force
ifeq ($(TARGET_OS),windows-6.0)
	$(eval GO_EXTRALDFLAGS = -static -static-libgcc -static-libstdc++)
endif
ifndef XGO_LOCAL
	xgo -go $(XGO_VERSION) -image=elgatito/xgo-latest -targets=$(TARGET_OS)/$(TARGET_ARCH) -dest $(BUILD_PATH) -ldflags='$(GO_LDFLAGS) -extldflags "$(GO_EXTRALDFLAGS)"' $(GO_PKG)
else
	xgo -go $(XGO_VERSION) -image=elgatito/xgo-latest -targets=$(TARGET_OS)/$(TARGET_ARCH) -dest $(BUILD_PATH) -ldflags='$(GO_LDFLAGS) -extldflags "$(GO_EXTRALDFLAGS)"' $(GOPATH)/src/$(GO_PKG)
endif
	./move-binaries.sh

strip: force
	@find $(BUILD_PATH) -type f ! -name "*.exe" -exec $(STRIP) {} \;

checksum: $(BUILD_PATH)/$(OUTPUT_NAME)
	shasum -b $(BUILD_PATH)/$(OUTPUT_NAME) | cut -d' ' -f1 >> $(BUILD_PATH)/$(OUTPUT_NAME)

dist: elementum strip checksum

binaries:
	git config --global push.default simple
	git clone --depth=1 https://github.com/elgatito/elementum-binaries binaries
	cp -Rf build/* binaries/
	cd binaries && git add * && git commit -m "Update to ${GIT_VERSION}"

pull:
	docker pull elgatito/xgo-latest