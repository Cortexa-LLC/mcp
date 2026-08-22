# kg hub container image.
#
# Build context must be the repo's src/ directory so the kg module's local
# `replace ../kglib` path resolves:
#
#   cd src && docker build -f kg/Dockerfile -t kg-hub .
#
# Run:
#   docker run -e KG_HUB_SEED_TOKEN=... -p 7411:7411 -v kg-hub-data:/data kg-hub

FROM golang:1.26 AS build
WORKDIR /src
# Copy both modules, preserving the ../kglib replace path.
COPY kglib/ kglib/
COPY kg/ kg/
WORKDIR /src/kg
RUN CGO_ENABLED=1 go build -o /out/kg .

FROM debian:stable-slim
# libstdc++6: the cgo Kuzu engine links the C++ runtime, which slim lacks
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git libstdc++6 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/kg /usr/local/bin/kg
VOLUME /data
EXPOSE 7411
ENTRYPOINT ["kg", "hub", "serve", "--data", "/data", "--listen", ":7411"]
