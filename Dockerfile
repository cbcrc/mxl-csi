# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS builder
WORKDIR /src/

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/ti-eng-mxl-k8s-csi .

FROM alpine:3.20

# mount/umount are required by NodePublishVolume and NodeUnpublishVolume.
RUN apk add --no-cache util-linux

COPY --from=builder /out/ti-eng-mxl-k8s-csi /usr/local/bin/ti-eng-mxl-k8s-csi

ENTRYPOINT ["/usr/local/bin/ti-eng-mxl-k8s-csi"]
