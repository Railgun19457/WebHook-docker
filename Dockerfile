FROM golang:1.23-alpine AS builder

ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/webhook-docker ./cmd/webhook-docker

FROM alpine:3.20

RUN addgroup -S app && adduser -S app -G app

WORKDIR /app

COPY --from=builder /out/webhook-docker /app/webhook-docker
COPY configs /app/config
COPY scripts /app/scripts

USER app

EXPOSE 8080

ENTRYPOINT ["/app/webhook-docker"]