FROM centos:7

LABEL org.opencontainers.image.title="OPF CentOS 7 fetch environment"
LABEL org.opencontainers.image.description="Ambiente isolado de testes/download YUM do Offline Package Fetcher"
LABEL org.opencontainers.image.version="f3"

ENV container=docker

# CentOS 7 está EOL; o default aponta para um espelho do centos-store.
# Pode ser sobrescrito em builds com --build-arg CENTOS_STORE=<url>.
ARG CENTOS_STORE=https://mirror.nsc.liu.se/centos-store

RUN sed -i \
        -e 's/^mirrorlist=/#mirrorlist=/' \
        -e "s|^#baseurl=http://mirror.centos.org/centos/|baseurl=${CENTOS_STORE}/|" \
        -e 's|\$releasever|7.9.2009|g' \
        /etc/yum.repos.d/CentOS-*.repo \
    && yum -y install yum-utils ca-certificates \
    && yum clean all

CMD ["sleep", "2147483647"]
