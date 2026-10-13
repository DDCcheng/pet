# ---- build ----
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pet ./server

# ---- run ----
FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/pet /app/pet
# 程序按相对路径 configs/cards.yaml 读卡表
COPY configs ./configs
EXPOSE 8080
ENTRYPOINT ["/app/pet"]
