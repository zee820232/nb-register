FROM docker.m.daocloud.io/library/golang:1.26-alpine AS builder

WORKDIR /app
ENV GOPROXY=https://goproxy.cn,direct

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o outlook-imap-service .

FROM docker.m.daocloud.io/library/alpine:latest

WORKDIR /app
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.ustc.edu.cn/g' /etc/apk/repositories && \
    apk add --no-cache ca-certificates tzdata
COPY --from=builder /app/outlook-imap-service /app/outlook-imap-service

EXPOSE 50051

ENTRYPOINT ["/app/outlook-imap-service"]
