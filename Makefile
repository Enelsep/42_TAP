.PHONY: deps webkit-dev build server cli gui gui-build lint test clean

WEBKIT_PREFIX ?= $(HOME)/.local/webkit-dev
WEBKIT_LIB    := $(WEBKIT_PREFIX)/usr/lib/x86_64-linux-gnu
WEBKIT_DEBS   := libwebkit2gtk-4.0-dev libjavascriptcoregtk-4.0-dev libsoup2.4-dev libpsl-dev
export PKG_CONFIG_PATH := $(WEBKIT_LIB)/pkgconfig:$(PKG_CONFIG_PATH)

deps:
	go mod download && cd gui && go mod download

webkit-dev:
	@mkdir -p $(WEBKIT_PREFIX)/debs
	cd $(WEBKIT_PREFIX)/debs && apt-get download $(WEBKIT_DEBS)
	@for d in $(WEBKIT_PREFIX)/debs/*.deb; do dpkg -x $$d $(WEBKIT_PREFIX); done
	@sed -i -e 's|^prefix=/usr$$|prefix=$(WEBKIT_PREFIX)/usr|' -e 's|^libdir=/usr/lib/x86_64-linux-gnu$$|libdir=$(WEBKIT_LIB)|' $(WEBKIT_LIB)/pkgconfig/*.pc
	@ln -sf /usr/lib/x86_64-linux-gnu/libwebkit2gtk-4.0.so.37      $(WEBKIT_LIB)/libwebkit2gtk-4.0.so
	@ln -sf /usr/lib/x86_64-linux-gnu/libjavascriptcoregtk-4.0.so.18 $(WEBKIT_LIB)/libjavascriptcoregtk-4.0.so
	@ln -sf /usr/lib/x86_64-linux-gnu/libsoup-2.4.so.1             $(WEBKIT_LIB)/libsoup-2.4.so
	@ln -sf /usr/lib/x86_64-linux-gnu/libpsl.so.5                  $(WEBKIT_LIB)/libpsl.so
	@pkg-config --exists webkit2gtk-4.0 && echo "webkit2gtk-4.0 $$(pkg-config --modversion webkit2gtk-4.0) ready"

build:
	go build -o bin/server ./core/cmd/server
	go build -o bin/cli    ./core/cmd/cli

server: build ; ./bin/server -addr :4242 -world data/world.json
cli:    build ; ./bin/cli    -addr localhost:4242

gui:
	cd gui && wails dev

gui-build:
	cd gui && wails build

lint:
	gofmt -l . && go vet ./core/...

test:
	go test -race ./core/...
	go test ./gui/...

clean:
	rm -rf bin gui/build
