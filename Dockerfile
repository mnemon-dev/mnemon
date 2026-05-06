# syntax=docker/dockerfile:1

ARG GO_VERSION=1.24.6

FROM golang:${GO_VERSION}-bookworm AS dev
RUN apt-get update \
  && apt-get install -y --no-install-recommends bash git jq make ca-certificates \
  && rm -rf /var/lib/apt/lists/*
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
CMD ["bash"]

FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
  -ldflags "-s -w -X github.com/mnemon-dev/mnemon/cmd.version=${VERSION}" \
  -o /out/mnemon .

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates tzdata \
  && addgroup -S mnemon \
  && adduser -S -G mnemon -h /home/mnemon mnemon \
  && mkdir -p /mnemon \
  && chown -R mnemon:mnemon /mnemon /home/mnemon
COPY --from=build /out/mnemon /usr/local/bin/mnemon
USER mnemon
ENV MNEMON_DATA_DIR=/mnemon \
    MNEMON_STORE=default
VOLUME ["/mnemon"]
ENTRYPOINT ["mnemon"]
CMD ["status"]
