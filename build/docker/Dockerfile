# syntax=docker/dockerfile:1

FROM golang:1.26.6-alpine AS builder
WORKDIR /src/

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/mxl-csi ./cmd/mxl-csi

FROM alpine:3.24

# mount/umount are required by NodePublishVolume and NodeUnpublishVolume.
RUN apk add --no-cache util-linux

COPY --from=builder /out/mxl-csi /usr/local/bin/mxl-csi

ENTRYPOINT ["/usr/local/bin/mxl-csi"]
