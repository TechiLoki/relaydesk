# Case study: reliable order handoff

## Problem

A small store needs paid orders copied to a fulfillment system. Retrying a failed request is easy; knowing whether a timeout happened before or after a destination write is harder. Duplicate source webhooks, out-of-order updates, and worker crashes make a naive forwarding script unreliable.

## Implementation

RelayDesk separates ingestion from delivery. Its Go API verifies a signed payload, normalizes the order, and invokes a C++ checked-arithmetic engine. A PostgreSQL RPC then records the event, updates the order, and inserts a delivery in one transaction. Repeating the same event acknowledges it without another job.

A Go worker claims the durable job with a fenced lease and forwards its immutable version using a stable Idempotency-Key. Transient responses schedule backoff; permanent failures remain visible for operator recovery. An internal PostgREST service gives Go a dependency-free HTTP database interface.

The TypeScript dashboard makes the behavior inspectable: latest source state, destination version, attempts, duplicate counts, and audit entries appear together. Operators can deliberately fail the synthetic destination and retry an exhausted job.

## Why C++ belongs here

Monetary validation has a narrow, testable interface and clear invariants. Integer multiplication/addition must stay within a supported bound, discounts cannot exceed the gross amount, and the declared total must match the full item breakdown. C++ implements those rules as a small executable; Go owns network and workflow concerns. No performance advantage is claimed for the process boundary.

## Evidence and limits

The repository contains Go unit/HTTP tests, C++ checks, TypeScript tests, a PostgreSQL integration transaction, and a real multi-container smoke test. The smoke scenario covers eight concurrent webhook replays, an acknowledgement lost after commit, version updates, an exhausted retry budget, and manual recovery.

Implementation-environment results are recorded in VALIDATION.md. Only the checks actually executed there should be described as passing. The fulfillment system is explicitly simulated; the Stripe adapter is test-mode capable but has not been validated against an authenticated Stripe account in that environment.

## Practical tradeoffs

A transactional database queue avoids a cross-system database/Redis write gap and keeps the demo small. Destination idempotency is part of the receiver contract, not a promise the sender can impose on any third-party API. The MVP is single-user and local; production identity, retention, and deployment work require a separate scope.
