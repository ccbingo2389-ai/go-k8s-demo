# 构建阶段
FROM crpi-qog7f6f2a152sq41.cn-shanghai.personal.cr.aliyuncs.com/test20260603/golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN  CGO_ENABLED=0 GOOS=linux go build -o server main.go

# 运行阶段
FROM crpi-qog7f6f2a152sq41.cn-shanghai.personal.cr.aliyuncs.com/test20260603/alpine:latest
WORKDIR /root/
COPY --from=builder /app/server .
EXPOSE 8080
CMD ["./server"]
