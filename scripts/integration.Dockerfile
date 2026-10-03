FROM alpine:3.21
# jq is used only by the test driver. The daemon runs with an empty tool PATH.
RUN apk add --no-cache jq bind-tools
ARG TARGETARCH
COPY bin/linux-${TARGETARCH}/meldnet /usr/local/bin/meldnet
COPY bin/linux-${TARGETARCH}/meldnetd /usr/local/bin/meldnetd
ENTRYPOINT ["/usr/local/bin/meldnetd"]
