FROM golang:1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Compile one package at a time and collect garbage more frequently on small hosts.
# GOMEMLIMIT is a soft target for each Go build process, not a container RAM cap.
RUN CGO_ENABLED=0 GOMAXPROCS=2 GOGC=20 GOMEMLIMIT=512MiB go build -p 1 -trimpath -ldflags='-s -w' -o /retro-radio ./cmd/retro-radio

FROM alpine:3.22
RUN apk add --no-cache ca-certificates ffmpeg && addgroup -g 10001 retro && adduser -D -u 10001 -G retro retro && mkdir -p /data /run/retro-upnp && chown retro:retro /data /run/retro-upnp
COPY --from=build /retro-radio /usr/local/bin/retro-radio
USER 10001:10001
ENV RETRO_DB=/data/retro-radio.db
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["retro-radio", "-healthcheck"]
ENTRYPOINT ["retro-radio"]
