# Build do site em container (nada instalado no host).
FROM node:22.23.3-slim AS build
WORKDIR /web
COPY package.json package-lock.json* ./
RUN if [ -f package-lock.json ]; then npm ci --no-audit --no-fund; else npm install --no-audit --no-fund; fi
COPY . .
RUN npm run test && npm run build

# Saída estática servida pelo nginx (read-only, sem porta publicada).
FROM nginx:1.30.5-alpine AS web
RUN rm -f /etc/nginx/conf.d/*.conf
COPY nginx/nginx.conf /etc/nginx/nginx.conf
COPY --from=build /web/dist /usr/share/nginx/html
