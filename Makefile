PROTOBUF_VERSION = 3.19.1

ifeq ($(UNAME),Darwin)
PROTOBUF_ZIP = protoc-$(PROTOBUF_VERSION)-osx-x86_64.zip
else
PROTOBUF_ZIP = protoc-$(PROTOBUF_VERSION)-linux-x86_64.zip
endif

tidy:
	go mod tidy

fmt:
	gofmt -w -s .
	goimports -w .

protoc3/bin/protoc:
	@# remove local folder
	rm -rf protoc3 || true

	@# Make sure you grab the latest version
	curl -OL https://github.com/protocolbuffers/protobuf/releases/download/v$(PROTOBUF_VERSION)/$(PROTOBUF_ZIP)

	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

	@# Unzip
	unzip $(PROTOBUF_ZIP) -d protoc3
	@# delete the files
	rm $(PROTOBUF_ZIP)

	@# move protoc to /usr/local/bin/
	chmod +x protoc3/bin/protoc

archipelago.pb.go: archipelago.proto protoc3/bin/protoc
	protoc3/bin/protoc --proto_path=. --go_out=. \
    --go_opt=paths=source_relative \
    --go_opt=Marchipelago.proto=github.com/decentraland/livekit-adapter/main \
		archipelago.proto 

build: archipelago.pb.go
	go build

start: build
	./livekit-adapter
