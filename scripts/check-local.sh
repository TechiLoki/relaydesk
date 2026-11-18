#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if command -v tsc >/dev/null 2>&1; then
  tsc -p web
else
  echo 'TypeScript compiler not found. Run npm install in web/, then npm run build.' >&2
  exit 1
fi
node web/build.mjs
node --test web/tests/*.test.mjs
node --check scripts/send-event.mjs
sh -n db/00-roles.sh
docker compose config --quiet
if command -v g++ >/dev/null 2>&1 && command -v go >/dev/null 2>&1; then
 mkdir -p .build
 g++ -std=c++20 -O2 -Wall -Wextra -Werror -pedantic rules/main.cpp -o .build/order-rules
 g++ -std=c++20 -O2 -Wall -Wextra -Werror -pedantic rules/test.cpp -o .build/rules-test
 .build/rules-test
 RULES_BIN="$(pwd)/.build/order-rules" go test -race ./...
 go vet ./...
else
 echo 'Go/C++ compilers not available: native/backend checks require docker compose build api.'
fi
