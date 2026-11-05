FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY packages/tamizhi ./packages/tamizhi
COPY apps/tamizhi-api ./apps/tamizhi-api
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tamizhi ./apps/tamizhi-api

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && addgroup -g 10001 tamizhi && adduser -D -u 10001 -G tamizhi tamizhi && mkdir /data && chown tamizhi:tamizhi /data
COPY --from=build /out/tamizhi /usr/local/bin/tamizhi
USER 10001:10001
WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["tamizhi", "api"]
CMD ["-config", "/config/tamizhi.json"]
