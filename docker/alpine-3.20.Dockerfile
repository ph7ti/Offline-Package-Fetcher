FROM alpine:3.20

LABEL org.opencontainers.image.title="OPF Alpine 3.20 fetch environment"
LABEL org.opencontainers.image.description="Ambiente isolado de testes/download APK do Offline Package Fetcher"
LABEL org.opencontainers.image.version="f4"

RUN apk add --no-cache ca-certificates

CMD ["sleep", "2147483647"]
