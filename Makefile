build:
	go build

lint:
	golint ./...

tidy:
	go mod tidy

fmt:
	gofmt -w -s .
	goimports -w .
