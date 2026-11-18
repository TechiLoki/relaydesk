FROM node:22-bookworm-slim AS frontend
WORKDIR /web
COPY web/package*.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund
COPY web/ ./
RUN npm run build && npm test

FROM gcc:12-bookworm AS native
WORKDIR /native
COPY rules/ ./
RUN mkdir /out && g++ -std=c++20 -O2 -Wall -Wextra -Werror -pedantic main.cpp -o /out/order-rules \
 && g++ -std=c++20 -O2 -Wall -Wextra -Werror -pedantic test.cpp -o /out/rules-test \
 && /out/rules-test

FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY --from=native /out/order-rules /usr/local/bin/order-rules
COPY . .
COPY --from=frontend /web/dist/ ./web/dist/
ENV RULES_BIN=/usr/local/bin/order-rules
RUN go vet ./... && go test -race ./... \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/relaydesk .

FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends libstdc++6 ca-certificates \
 && rm -rf /var/lib/apt/lists/* && useradd --uid 10001 --create-home relaydesk
WORKDIR /app
COPY --from=build /out/relaydesk /usr/local/bin/relaydesk
COPY --from=native /out/order-rules /usr/local/bin/order-rules
USER relaydesk
EXPOSE 8080
ENTRYPOINT ["relaydesk"]
