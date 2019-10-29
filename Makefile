.PHONY: all build dist test install

build:
	@mkdir -p ./bin && rm -f ./bin/*
	go build -o ./bin/hydrate ./cmd/hydrate

dist:
	@mkdir -p ./bin && rm -f ./bin/*
	GOOS=darwin GOARCH=amd64 go build -o ./bin/hydrate-darwin64 ./cmd/hydrate
	GOOS=linux GOARCH=amd64 go build -o ./bin/hydrate-linux64 ./cmd/hydrate
	GOOS=linux GOARCH=386 go build -o ./bin/hydrate-linux386 ./cmd/hydrate
	GOOS=windows GOARCH=amd64 go build -o ./bin/hydrate-windows64.exe ./cmd/hydrate
	GOOS=windows GOARCH=386 go build -o ./bin/hydrate-windows386.exe ./cmd/hydrate

test:
	go test ./...

install:
	go install ./cmd/hydrate
