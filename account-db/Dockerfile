FROM docker.m.daocloud.io/library/golang:1.26-alpine AS builder

WORKDIR /app

# Enable go module proxy
ENV GOPROXY=https://goproxy.cn,direct

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN go build -o account-db main.go

FROM docker.m.daocloud.io/library/alpine:latest

# Use domestic apk mirrors
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories

WORKDIR /app
COPY --from=builder /app/account-db .

EXPOSE 50051

CMD ["./account-db"]
