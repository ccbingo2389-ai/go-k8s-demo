# 构建阶段
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o server main.go

# 运行阶段
FROM http://registry.cn-shanghai.aliyuncs.com/zhanghaoyu001/alpine:latest
WORKDIR /root/
COPY --from=builder /app/server .
EXPOSE 8080
CMD ["./server"]
