# The release source is pinned to the verified multi-architecture server build.
# Only its static HBBR binary is copied into the final image.
FROM blackxdog/remote-control-server-ros:2.2.1@sha256:e1ad1e4a46601d935082114d21e09892915376c20f6449dba9605a67eef447b8 AS release

FROM scratch
COPY --from=release --chmod=0555 /usr/local/bin/art-hbbr /art-hbbr
USER 65532:65532
ENV RDS_INTERNAL_SECRET_FILE=/run/secrets/internal.secret
LABEL org.opencontainers.image.title="Remote Control Relay" \
      org.opencontainers.image.version="2.2.1" \
      org.opencontainers.image.description="Standalone authenticated HBBR relay for Remote Control Server"
EXPOSE 21117/tcp 21119/tcp
ENTRYPOINT ["/art-hbbr"]
