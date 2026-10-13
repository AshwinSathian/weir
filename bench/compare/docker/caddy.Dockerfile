# Caddy with the plugins named in WITH (xcaddy --with flags), from published
# module versions only. GOTOOLCHAIN=auto lets the build fetch Go 1.27, which
# Weir needs (docs/08).
FROM caddy:2.11.7-builder AS build
ARG WITH=""
ENV GOTOOLCHAIN=auto
RUN xcaddy build v2.11.7 ${WITH} --output /caddy

FROM caddy:2.11.7
COPY --from=build /caddy /usr/bin/caddy
