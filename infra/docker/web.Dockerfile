# Build context is the repository root.
#
#   docker build -f infra/docker/web.Dockerfile .

FROM node:26-alpine AS build

# package.json and the lockfile are copied first so that `npm ci` is cached independently of
# source changes.
WORKDIR /src
COPY apps/web/package.json apps/web/package-lock.json ./
RUN npm ci

COPY apps/web/ ./
RUN npm run build

# ---------------------------------------------------------------------------

FROM nginx:alpine

COPY infra/docker/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist/web/browser /usr/share/nginx/html

EXPOSE 80
