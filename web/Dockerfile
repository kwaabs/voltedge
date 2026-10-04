# web/Dockerfile -- portable SPA image: build the Vite/React bundle and serve it
# with nginx. The built bundle is BAKED IN, which is what makes this image work
# where a host bind-mount cannot -- Coolify and Kubernetes both build images,
# they do not mount a host-built web/dist. (The dev stack and the host-built
# docker-compose.prod.yml can still bind-mount ./web/dist against a plain nginx;
# this image is for the platforms that can't.)
#
# The nginx CONFIG is deliberately NOT baked -- provide it externally
# (a compose bind-mount, or a Kubernetes ConfigMap mounted at
# /etc/nginx/nginx.conf). The upstream-resolution strategy differs between
# Docker Compose (Docker DNS + the resolver trick) and Kubernetes (a Service
# gives stable DNS + pod load-balancing, so a literal upstream is correct) --
# see docs/kubernetes-and-coolify-guide.md for both configs.
#
# Package manager: this uses npm (package-lock.json). If the repo uses bun or
# pnpm, swap the install/build lines accordingly.

FROM node:20-alpine AS build
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci
COPY . .
RUN npm run build          # -> /app/dist

FROM nginx:1.27-alpine
# The SPA bundle, served as static files. index.html + hashed /assets.
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
# No CMD override -- the base nginx image runs nginx; the proxy config is mounted in.
