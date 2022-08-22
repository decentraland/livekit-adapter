lint:
	golint ./...

tidy:
	go mod tidy

fmt:
	gofmt -w -s .
	goimports -w .

install-protoc:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

out/archipelago.pb.go: archipelago.proto install-protoc
	protoc --proto_path=. --go_out=. \
    --go_opt=paths=source_relative \
    --go_opt=Marchipelago.proto=github.com/decentraland/livekit-adapter/main \
		archipelago.proto 

build: archipelago.pb.go
	go build
