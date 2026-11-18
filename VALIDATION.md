# Verification record

## Verified on GitHub Actions

[Build and reliability tests — successful run](https://github.com/TechiLoki/relaydesk/actions/runs/44299083434)

Tested source commit: `9e8f9fea500b47c4c0085d7d17df591335e80be5`.

The successful run completed:

- Docker Compose configuration validation.
- TypeScript compilation and browser-independent Node tests.
- C++20 compilation with warnings treated as errors, and 12 arithmetic checks.
- Go vet, race-enabled Go tests, and the Go/C++ subprocess contract test.
- Full runtime image build and container startup.
- PostgreSQL integration assertions for deduplication, conflicts, idempotency, version ordering, lease fencing, retry recovery, dead-lettering, and role permissions.
- Real HTTP smoke proof: eight concurrent webhook replays; a destination commit whose reply is lost; exactly one destination commit; a version update; five failed requests during an outage; and manual recovery with attempt history retained.

## Also checked locally

TypeScript 5.9.3 strict compilation, generated assets, five browser-independent utility cases, 44 static DOM references, CLI/shell syntax, and Compose configuration passed. Runtime artifacts and the private .env are excluded from source publication.

## Not verified

- Browser visual/interaction testing.
- An actual Stripe CLI/test-account delivery.
- A live external fulfillment API, production deployment, or load/performance benchmark.

Docker and native compilers are restricted in the local sandbox; native and container checks were instead verified by the successful GitHub Actions run above. The fulfillment receiver remains an explicitly simulated integration.
