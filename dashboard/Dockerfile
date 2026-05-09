FROM docker.m.daocloud.io/library/node:22-bookworm-slim AS web-builder

WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm install
COPY web .
RUN npm run build

FROM docker.m.daocloud.io/library/golang:1.26-alpine AS go-builder

WORKDIR /app
ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=web-builder /web/dist ./web/dist
RUN go build -o dashboard .

FROM docker.m.daocloud.io/library/alpine:latest

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories && apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=go-builder /app/dashboard .
COPY --from=go-builder /app/web/dist ./web/dist

EXPOSE 8080
CMD ["./dashboard"]
