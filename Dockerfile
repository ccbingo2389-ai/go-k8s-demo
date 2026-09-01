# ========== Build Stage ==========
FROM crpi-qog7f6f2a152sq41.cn-shanghai.personal.cr.aliyuncs.com/test20260603/golang:1.26-alpine AS builder

# 国内 Go 模块代理，避免 proxy.golang.org 连不上
ENV GOPROXY=https://goproxy.cn,direct

WORKDIR /app

# 先复制依赖文件，利用 Docker 层缓存
COPY go.mod go.sum ./
RUN go mod download

# 再复制源码并构建
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o server main.go

# ========== Runtime Stage ==========
FROM crpi-qog7f6f2a152sq41.cn-shanghai.personal.cr.aliyuncs.com/test20260603/alpine:latest

# 换阿里云 apk 源 + 安装 CA 证书和时区
RUN sed -i 's|dl-cdn.alpinelinux.org|mirrors.aliyun.com|g' /etc/apk/repositories \
    && apk --no-cache add ca-certificates tzdata
ENV TZ=Asia/Shanghai

WORKDIR /root/
COPY --from=builder /app/server .

EXPOSE 8080
CMD ["./server"]
