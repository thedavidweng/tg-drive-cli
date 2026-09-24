# 0022: E2E-First Testing

Status: Accepted

Context: CRUD happy-path tests duplicated fake-Telegram E2E. Envelope, contract-mirror, slug-echo, and render-parse tests asserted constants via string matching. Coverage patch target drove test creation.

Decision: Fake-Telegram E2E plus integration tests are the default with lifecycle, resume, rollback, orphan, and idempotency artifacts. Isolated tests remain only for truncation budgets, permission denials, rollback scopes, lock races, migration backfills, retry bounds, and error mapping with exact codes. Coverage informational: patch 80 to 50, threshold 2 to 5.

Consequences: Deleted 8 files and 70 echo tests (39 to 31 files). loadPathFile, loadPathCases, roundTrip helpers, and hashtagAlnum removed with their tests.
