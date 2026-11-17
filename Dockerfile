FROM node:22-alpine AS ui
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -o /out/testplane ./cmd/testplane

FROM alpine:3.20
RUN adduser -D testplane
USER testplane
COPY --from=build /out/testplane /usr/local/bin/testplane
VOLUME /workspace
EXPOSE 8080
ENTRYPOINT ["testplane"]
CMD ["serve", "--workspace", "/workspace", "--addr", "0.0.0.0:8080"]
