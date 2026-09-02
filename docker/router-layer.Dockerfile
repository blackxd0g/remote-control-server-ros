ARG CURRENT_IMAGE=blackxdog/rustdesk-server-routeros:latest
ARG BASE_IMAGE=blackxdog/rustdesk-server-routeros:1.1.0

FROM ${CURRENT_IMAGE} AS current
FROM ${BASE_IMAGE}

# Keep all application binaries in one incremental layer. RouterOS has a strict
# layer limit, and auth/protocol releases may change HBBS alongside API/Web.
COPY --from=current /usr/local/bin/art-api /usr/local/bin/art-hbbs /usr/local/bin/art-hbbr /usr/local/bin/
