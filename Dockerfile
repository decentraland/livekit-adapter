ARG RUN

FROM golang:1.19 as builderenv

WORKDIR /app

# some packages require a build step
RUN apt-get update
RUN apt-get -y -qq install build-essential unzip

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

COPY archipelago.proto archipelago.proto
COPY main.go main.go
COPY Makefile Makefile

ENV GOBIN=/go/bin
ENV PATH=$PATH:$GOBIN
RUN make build

########################## END OF BUILD STAGE ##########################

FROM golang:1.19

ARG COMMIT_HASH=local
ENV COMMIT_HASH=${COMMIT_HASH:-local}

WORKDIR /app
COPY --from=builderenv /app/livekit-adapter /app/livekit-adapter

ENTRYPOINT ["/app/livekit-adapter"]
