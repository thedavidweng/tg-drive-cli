# Changelog

## [0.2.0](https://github.com/thedavidweng/tg-drive-cli/compare/v0.1.0...v0.2.0) (2026-09-17)


### Features

* adopt native albums with one reconstructable inventory ([4303eff](https://github.com/thedavidweng/tg-drive-cli/commit/4303eff517a61e7b5f71839f25aec14a7621d5c8))
* **cp,share:** include invite link in upload output and read existing invite ([7136dd1](https://github.com/thedavidweng/tg-drive-cli/commit/7136dd17a1bfccc17e00a0d639d5d207efc7e890))
* import Saved Messages into the drive ([4c12f74](https://github.com/thedavidweng/tg-drive-cli/commit/4c12f7407ff9e01546b64d920b486caec221c6d2))
* ls --json exposes stored content hash ([#31](https://github.com/thedavidweng/tg-drive-cli/issues/31)) ([b8fec5e](https://github.com/thedavidweng/tg-drive-cli/commit/b8fec5ef096ebc9110d8b5259caecfb401422830))
* native album uploads and discussion-thread machine records (ADR 0018) ([#34](https://github.com/thedavidweng/tg-drive-cli/issues/34)) ([28c4a8f](https://github.com/thedavidweng/tg-drive-cli/commit/28c4a8f215a734baa66ebee7c36c5c09c07257fe))
* production hardening — recovery correctness, wired resume, concurrency, scan scale ([47d1b7a](https://github.com/thedavidweng/tg-drive-cli/commit/47d1b7a4b5a291adb174555b4bbc8ccf27bfb088))
* rework login flow and overhaul CLI UX from cold-user audit ([edee40a](https://github.com/thedavidweng/tg-drive-cli/commit/edee40a457b0bdb6b5e6c1a97b493458a54fec53))
* **telegramgotd:** add proactive per-method rate limiter and channel title cache ([fe47ac8](https://github.com/thedavidweng/tg-drive-cli/commit/fe47ac8ee349c8b9ad1e8dd76af98864609c2392))
* typed uploads with native photo kind ([#32](https://github.com/thedavidweng/tg-drive-cli/issues/32)) ([c577439](https://github.com/thedavidweng/tg-drive-cli/commit/c577439a406bdb9f5df4aa63261a5020d7a19c73))
* typed uploads with video presentation attributes ([#30](https://github.com/thedavidweng/tg-drive-cli/issues/30)) ([fc677a9](https://github.com/thedavidweng/tg-drive-cli/commit/fc677a9ddef60921907f76c47912e8eea61c8a15))


### Bug Fixes

* align product spec, contracts, schema, and complete release-readiness gaps ([68a36a2](https://github.com/thedavidweng/tg-drive-cli/commit/68a36a2a43556aad84adaa24e6025886b1c6a736))
* **telegramgotd:** avoid VOLUME_LOC_NOT_FOUND by disabling gotd downloader verifier ([414faf3](https://github.com/thedavidweng/tg-drive-cli/commit/414faf3e38d7ebfca224ff74dea3ea56500b469d))
* **telegramgotd:** stop dialog iteration once the target channel is resolved ([ce2e321](https://github.com/thedavidweng/tg-drive-cli/commit/ce2e3214d0798e332bf36d9808473f943037b3d1))


### Refactoring

* extract deep modules and ports from the command layer ([36fba26](https://github.com/thedavidweng/tg-drive-cli/commit/36fba26adfd68e50a1c5e94ccebf5e092ee9ae99))
* fix PR [#34](https://github.com/thedavidweng/tg-drive-cli/issues/34) review findings ([#40](https://github.com/thedavidweng/tg-drive-cli/issues/40)) ([1c49a46](https://github.com/thedavidweng/tg-drive-cli/commit/1c49a46768a1f1f64c5be091ef171855a659550f))
* rename import to adopt; reserve import for external-chat ingest (ADR 0019) ([c0846c3](https://github.com/thedavidweng/tg-drive-cli/commit/c0846c3ff6dbefacb51281a933ab7a5b79ecb3c9))


### Documentation

* add Diataxis user guides under docs/guides/ ([d3d717b](https://github.com/thedavidweng/tg-drive-cli/commit/d3d717bcbc905f5d182339c03b7192af540e152d))
* drop stale plans and rewrite public docs for release ([4bcd7f4](https://github.com/thedavidweng/tg-drive-cli/commit/4bcd7f49673b99dc9d0fff69af5d46740b785777))

## Changelog

Managed by release-please.
