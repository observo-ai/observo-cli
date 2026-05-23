# GoReleaser injects the pre-built binary at /observo via the dockers
# block in .goreleaser.yaml — no compile happens here. Multi-arch is
# handled by separate GoReleaser docker entries (one per goarch) + the
# docker_manifests block that stitches them into a single tag.
#
# We use distroless/static (not scratch) for the cert bundle — the CLI
# makes HTTPS calls to api.observoai.co; without ca-certs it fails with
# x509 errors on first request.
FROM gcr.io/distroless/static-debian12:nonroot

COPY observo /observo

USER nonroot:nonroot
ENTRYPOINT ["/observo"]
