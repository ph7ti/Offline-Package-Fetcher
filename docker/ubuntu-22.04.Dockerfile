FROM ubuntu:22.04

LABEL org.opencontainers.image.title="OPF Ubuntu 22.04 fetch environment"
LABEL org.opencontainers.image.description="Ambiente isolado de testes/download APT do Offline Package Fetcher"
LABEL org.opencontainers.image.version="f0"

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

CMD ["sleep", "infinity"]
