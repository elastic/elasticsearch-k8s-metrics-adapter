# Minimal image for the mock Elasticsearch used by e2e tests.
# The binary is built on the host (see the `mockes-image` Make target) for the
# target arch and copied into scratch — mockes is stdlib-only and serves plain
# HTTP, so it needs no certs or shell.
FROM scratch
COPY mockes /mockes
EXPOSE 9200
ENTRYPOINT ["/mockes"]
