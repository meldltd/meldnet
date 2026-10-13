FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends nsis \
    && rm -rf /var/lib/apt/lists/*
ENTRYPOINT ["makensis"]
