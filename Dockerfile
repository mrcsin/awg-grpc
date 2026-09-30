# syntax=docker/dockerfile:1

ARG ALPINE_IMAGE=alpine:3.24.2
ARG GOLANG_IMAGE=golang:1.27.1-alpine3.24

# amneziawg-tools at the commit that matches the host module generation. Without .git the
# Makefile skips git describe and the version comes from src/version.h.
FROM ${ALPINE_IMAGE} AS tools
ARG AWG_TOOLS_REF=ee0f0a9aa34ff0a0da4b3433b9512781cfe02843
RUN apk add --no-cache build-base git linux-headers
RUN git clone https://github.com/amnezia-vpn/amneziawg-tools.git /src \
 && git -C /src checkout --detach "$AWG_TOOLS_REF" \
 && rm -rf /src/.git
RUN make -C /src/src \
 && make -C /src/src install DESTDIR=/out \
      WITH_WGQUICK=yes WITH_BASHCOMPLETION=no WITH_SYSTEMDUNITS=no

FROM ${GOLANG_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY gen ./gen
COPY internal ./internal
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/awg-grpc ./cmd/awg-grpc

FROM ${GOLANG_IMAGE} AS integration
RUN apk add --no-cache bash iproute2
COPY --from=tools /out/ /
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
CMD ["go", "test", "-tags", "integration", "-count=1", "./..."]

FROM ${ALPINE_IMAGE} AS runtime
RUN apk add --no-cache bash iproute2 iptables
COPY --from=tools /out/ /
COPY --from=build /out/awg-grpc /usr/local/bin/awg-grpc
# serve does not create the socket directory; an empty volume mounted here copies it.
RUN mkdir -p /run/awg-grpc
ENTRYPOINT ["awg-grpc", "serve"]
HEALTHCHECK --interval=10s --timeout=5s --retries=3 CMD ["awg-grpc", "healthcheck"]
