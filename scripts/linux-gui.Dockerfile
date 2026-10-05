FROM postgres:17-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends python3 python3-gi gir1.2-gtk-3.0 gir1.2-ayatanaappindicator3-0.1 xvfb xauth fonts-dejavu-core librsvg2-common && rm -rf /var/lib/apt/lists/*
ENTRYPOINT []
WORKDIR /work
