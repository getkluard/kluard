FROM golang:1.26 AS build
WORKDIR /src
COPY . .
ARG VERSION=dev
# The version identifies the source code: the same source gives the same version on every server and in every agent,
# so the web UI and the Hosts page can show whether a server or an agent is up to date. Override with --build-arg VERSION=1.2.3
RUN set -e; \
    if [ "$VERSION" = dev ]; then \
      VERSION="src-$(find . -type f \( -name '*.go' -o -name '*.html' -o -name go.mod \) ! -name '*_test.go' | LC_ALL=C sort | xargs cat | sha256sum | cut -c1-8)"; \
    fi; \
    echo "$VERSION" > /version
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/getkluard/kluard/internal/buildinfo.Version=$(cat /version)" -o /kluard ./cmd/kluard
# agent binaries served by the server at /download, plus checksums. The same files are also offered under the old names
# (lumen-agent-<os>-<arch>): agents installed when the product was called Lumen ask for those when they update themselves.
RUN set -e; mkdir /dist; V="$(cat /version)"; \
    for t in linux/amd64 linux/arm64 windows/amd64 windows/arm64; do \
      os=${t%/*}; arch=${t#*/}; ext=""; [ "$os" = windows ] && ext=".exe"; \
      CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
        -ldflags "-s -w -X github.com/getkluard/kluard/internal/agent.Version=$V" \
        -o /dist/kluard-agent-$os-$arch$ext ./cmd/kluard-agent; \
    done; \
    for f in /dist/kluard-agent-*; do cp "$f" "$(echo "$f" | sed 's#/kluard-agent-#/lumen-agent-#')"; done; \
    mkdir /data /backup; cd /dist && sha256sum kluard-agent-* lumen-agent-* > /tmp/SHA256SUMS && mv /tmp/SHA256SUMS /dist/SHA256SUMS

FROM gcr.io/distroless/static-debian12:nonroot
ENV KLUARD_DATA_DIR=/data
COPY --from=build /kluard /kluard
COPY --from=build --chown=65532:65532 /data /data
COPY --from=build --chown=65532:65532 /backup /backup
COPY --from=build /dist /dist
EXPOSE 4318
ENTRYPOINT ["/kluard"]
