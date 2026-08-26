.PHONY: deps build server cli gui lint test clean

deps:
	go mod download && cd gui && go mod download

build:
	go build -o bin/server ./core/cmd/server
	go build -o bin/cli    ./core/cmd/cli

server: build ; ./bin/server -addr :4242 -world data/world.json
cli:    build ; ./bin/cli    -addr localhost:4242

gui:
	cd gui && wails dev -tags webkit2_41

lint:
	gofmt -l . && go vet ./core/...

test:
	go test -race ./core/...

clean:
	rm -rf bin gui/build
