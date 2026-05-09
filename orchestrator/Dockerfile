FROM docker.m.daocloud.io/library/golang:1.26-alpine AS builder

WORKDIR /app

# Enable go module proxy
ENV GOPROXY=https://goproxy.cn,direct

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the whole package. Building main.go alone misses workflow/activity files
# that live in the same package.
RUN go build -o orchestrator .

FROM docker.m.daocloud.io/library/alpine:latest

# Use domestic apk mirrors
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories

WORKDIR /app
COPY --from=builder /app/orchestrator .

EXPOSE 50051

CMD ["./orchestrator"]
