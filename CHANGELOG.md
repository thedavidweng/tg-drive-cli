# Changelog

## [0.3.0](https://github.com/thedavidweng/tg-drive/compare/v0.2.1...v0.3.0) (2026-10-03)


### Features

* Ctrl-C cancels commands; loops stop between items ([#84](https://github.com/thedavidweng/tg-drive/issues/84)) ([59d0dde](https://github.com/thedavidweng/tg-drive/commit/59d0ddedd4f2d74276258e35f292bf7d52d76a65))
* every upload and download kind runs as a Transfer ([#87](https://github.com/thedavidweng/tg-drive/issues/87)) ([62edec9](https://github.com/thedavidweng/tg-drive/commit/62edec9d104d69b73ea8612b000316ddc5987c94))
* GUI channel binding, switching, and status ([#98](https://github.com/thedavidweng/tg-drive/issues/98)) ([2e366f5](https://github.com/thedavidweng/tg-drive/commit/2e366f5f11ef874944da89f72327d56a2237bd9c))
* GUI Drive tab with browsing, sheets, scan, and live sync ([#97](https://github.com/thedavidweng/tg-drive/issues/97)) ([e7cad5e](https://github.com/thedavidweng/tg-drive/commit/e7cad5e5590be686c592034097ba6d6e2cb20e9a))
* GUI Import and Maintenance tabs ([#100](https://github.com/thedavidweng/tg-drive/issues/100)) ([fb2ec0b](https://github.com/thedavidweng/tg-drive/commit/fb2ec0b90addf5ae5f610baf6e28b2415c892815))
* GUI Settings tab with secrets, theme, language, and Omarchy mode ([#92](https://github.com/thedavidweng/tg-drive/issues/92)) ([1a00c6a](https://github.com/thedavidweng/tg-drive/commit/1a00c6aa9fe9e60dcd0190dd97030cec292778ae))
* GUI setup and login with prompt events ([#94](https://github.com/thedavidweng/tg-drive/issues/94)) ([de5ede4](https://github.com/thedavidweng/tg-drive/commit/de5ede4eae2191e0c08a5da1c2d5537e2aa904a2))
* GUI Transfers tab with upload and download entry points ([#99](https://github.com/thedavidweng/tg-drive/issues/99)) ([e43ec1f](https://github.com/thedavidweng/tg-drive/commit/e43ec1f10d7b39c4e74ebfa25b69ee455c88401a))
* GUI upload and download option sheets with dry-run plan ([#102](https://github.com/thedavidweng/tg-drive/issues/102)) ([daeb841](https://github.com/thedavidweng/tg-drive/commit/daeb84114a08aca1aa71c510ba08a873fa76fc31))
* Session lock and atomic session storage ([#82](https://github.com/thedavidweng/tg-drive/issues/82)) ([124f854](https://github.com/thedavidweng/tg-drive/commit/124f8542a87abe5325a9272f16aa09ec5000297e))
* single-file td cp runs as a Transfer in the shared index ([#85](https://github.com/thedavidweng/tg-drive/issues/85)) ([8fd16ae](https://github.com/thedavidweng/tg-drive/commit/8fd16ae61699cb7af284abed1b34c072b0e962a9))
* td transfers retry with resume from saved upload parts ([#93](https://github.com/thedavidweng/tg-drive/issues/93)) ([f320e8a](https://github.com/thedavidweng/tg-drive/commit/f320e8ad5659af670ee93726fea18c9fce73b48f))
* td transfers watch and 30-day Transfer retention ([#89](https://github.com/thedavidweng/tg-drive/issues/89)) ([46a17a0](https://github.com/thedavidweng/tg-drive/commit/46a17a0d1f1c5ddf60a47ffc7cd680a79e428507))
* td-gui desktop skeleton with a read-only Drive list ([#86](https://github.com/thedavidweng/tg-drive/issues/86)) ([3ff6e90](https://github.com/thedavidweng/tg-drive/commit/3ff6e90ca8c35afb40d2ffdd31630e6ac1c0336d))
* Transfer ownership lease, interruption, and cross-process cancel ([#88](https://github.com/thedavidweng/tg-drive/issues/88)) ([816b7b5](https://github.com/thedavidweng/tg-drive/commit/816b7b508d682fb8765d23d2860d5448942b2b4e))
* tray, single instance, quit confirmation, and keyboard access for td-gui ([#101](https://github.com/thedavidweng/tg-drive/issues/101)) ([efd2bce](https://github.com/thedavidweng/tg-drive/commit/efd2bce4223183ce71f7c1cad266544a513d6267))


### Bug Fixes

* **gui:** close the [#110](https://github.com/thedavidweng/tg-drive/issues/110) acceptance gaps ([#112](https://github.com/thedavidweng/tg-drive/issues/112)) ([eaf5d63](https://github.com/thedavidweng/tg-drive/commit/eaf5d63cf5a89490f30f97b71417e0b05ed1ab85))
* **gui:** regenerate method bindings after module rename ([#111](https://github.com/thedavidweng/tg-drive/issues/111)) ([d769f9a](https://github.com/thedavidweng/tg-drive/commit/d769f9aa9e04bf06c9df15b909375af604b357ac))
* harden diagnostics, privacy, retries, and first-run UX for release ([4b634c6](https://github.com/thedavidweng/tg-drive/commit/4b634c6df6976186907e274ebc4ac13d867e8cb5))
* keep album inventory when replacing an album member ([ca78284](https://github.com/thedavidweng/tg-drive/commit/ca782841e165094a312f937fac67eaf108160a6d))
* keep the comment carrier when moving an album member ([d26eb4f](https://github.com/thedavidweng/tg-drive/commit/d26eb4fb46d820987be4db4d461ae597d7c684af))
* report new message ids for lone saved-album members ([bcfe440](https://github.com/thedavidweng/tg-drive/commit/bcfe440d85ace4f8ba906a8302c4442064372e9e))


### Refactoring

* album-aware file record module for rm, mv, repair, replace ([320dd19](https://github.com/thedavidweng/tg-drive/commit/320dd19d0ae9ca36b794ef6488aa988b10758a6e))
* drop browser-target seams and WASM gate ([dddec22](https://github.com/thedavidweng/tg-drive/commit/dddec22520d6ccdd05c69efd4227855a3672acee))
* intent-shaped File publisher entry points ([27c4b7f](https://github.com/thedavidweng/tg-drive/commit/27c4b7f9e8c761dff3102316d6157951f22dd779))
* move file row lifecycle into sqlitestore ([b6ee6ae](https://github.com/thedavidweng/tg-drive/commit/b6ee6ae2f69d758340aa3a8734b2243a3087da36))
* observer for download, scan, adopt, repair, and import ([#83](https://github.com/thedavidweng/tg-drive/issues/83)) ([22fa42b](https://github.com/thedavidweng/tg-drive/commit/22fa42b94c15e49b98f5afbb7a1860c5840521a0))
* one upload pipeline for single, album, and import uploads ([c54aed1](https://github.com/thedavidweng/tg-drive/commit/c54aed15ea7d2da7111d12b3d5d961a3ad4fba98))
* Operation module owns locks and channel context ([6e8b92b](https://github.com/thedavidweng/tg-drive/commit/6e8b92be36e680ec2e85e6b42d67bdc99285ab89))
* per-call channel, upload options, and upload observer ([#79](https://github.com/thedavidweng/tg-drive/issues/79)) ([0f224a2](https://github.com/thedavidweng/tg-drive/commit/0f224a25a2b35bb6f553e0296f1cd7689fb0cedc))
* rename the project to tg-drive ([f709c55](https://github.com/thedavidweng/tg-drive/commit/f709c559f748060f4efa0cf9e14b35e53fe9020b))
* rename the project to tg-drive ([82419ef](https://github.com/thedavidweng/tg-drive/commit/82419ef3af4febbd38b77cf9de258cd978dab8c8))
* return typed results from service use cases ([eb4fbd0](https://github.com/thedavidweng/tg-drive/commit/eb4fbd00f63ef38a6d00e074a7dac50fc16589b3))
* service enforces confirmation gates and repair modes ([#80](https://github.com/thedavidweng/tg-drive/issues/80)) ([a2296dc](https://github.com/thedavidweng/tg-drive/commit/a2296dc2544d99a15dea4c42c15fca482af581a0))
* service owns dry-run plan, Telegram setup, config, and init choices ([#81](https://github.com/thedavidweng/tg-drive/issues/81)) ([5b8fd42](https://github.com/thedavidweng/tg-drive/commit/5b8fd4263cc544387f8e163b34cbef4e688a99d4))
* service.Open is the one composition root ([#78](https://github.com/thedavidweng/tg-drive/issues/78)) ([30ed823](https://github.com/thedavidweng/tg-drive/commit/30ed82399afa9202162c78ee713e70d0cc532810))


### Documentation

* accept ADR 0036 — the preview pipeline publishes on every GUI PR ([#103](https://github.com/thedavidweng/tg-drive/issues/103)) ([3e4ff60](https://github.com/thedavidweng/tg-drive/commit/3e4ff6068f3dc53dd2e4e14d1ce663a2b4f3ae0a))
* ADRs and glossary for the desktop GUI and Transfers ([e158ff2](https://github.com/thedavidweng/tg-drive/commit/e158ff24f7da74bc7620aa2f36b9b35727531b66))

## [0.2.1](https://github.com/thedavidweng/tg-drive/compare/v0.2.0...v0.2.1) (2026-09-17)


### Bug Fixes

* keep internal paths out of modern captions ([2780831](https://github.com/thedavidweng/tg-drive/commit/2780831c15b3d0a9090160784d70caebbd4b81ad))

## [0.2.0](https://github.com/thedavidweng/tg-drive/compare/v0.1.0...v0.2.0) (2026-09-17)


### Features

* adopt native albums with one reconstructable inventory ([4303eff](https://github.com/thedavidweng/tg-drive/commit/4303eff517a61e7b5f71839f25aec14a7621d5c8))
* **cp,share:** include invite link in upload output and read existing invite ([7136dd1](https://github.com/thedavidweng/tg-drive/commit/7136dd17a1bfccc17e00a0d639d5d207efc7e890))
* import Saved Messages into the drive ([4c12f74](https://github.com/thedavidweng/tg-drive/commit/4c12f7407ff9e01546b64d920b486caec221c6d2))
* ls --json exposes stored content hash ([#31](https://github.com/thedavidweng/tg-drive/issues/31)) ([b8fec5e](https://github.com/thedavidweng/tg-drive/commit/b8fec5ef096ebc9110d8b5259caecfb401422830))
* native album uploads and discussion-thread machine records (ADR 0018) ([#34](https://github.com/thedavidweng/tg-drive/issues/34)) ([28c4a8f](https://github.com/thedavidweng/tg-drive/commit/28c4a8f215a734baa66ebee7c36c5c09c07257fe))
* production hardening — recovery correctness, wired resume, concurrency, scan scale ([47d1b7a](https://github.com/thedavidweng/tg-drive/commit/47d1b7a4b5a291adb174555b4bbc8ccf27bfb088))
* rework login flow and overhaul CLI UX from cold-user audit ([edee40a](https://github.com/thedavidweng/tg-drive/commit/edee40a457b0bdb6b5e6c1a97b493458a54fec53))
* **telegramgotd:** add proactive per-method rate limiter and channel title cache ([fe47ac8](https://github.com/thedavidweng/tg-drive/commit/fe47ac8ee349c8b9ad1e8dd76af98864609c2392))
* typed uploads with native photo kind ([#32](https://github.com/thedavidweng/tg-drive/issues/32)) ([c577439](https://github.com/thedavidweng/tg-drive/commit/c577439a406bdb9f5df4aa63261a5020d7a19c73))
* typed uploads with video presentation attributes ([#30](https://github.com/thedavidweng/tg-drive/issues/30)) ([fc677a9](https://github.com/thedavidweng/tg-drive/commit/fc677a9ddef60921907f76c47912e8eea61c8a15))


### Bug Fixes

* align product spec, contracts, schema, and complete release-readiness gaps ([68a36a2](https://github.com/thedavidweng/tg-drive/commit/68a36a2a43556aad84adaa24e6025886b1c6a736))
* **telegramgotd:** avoid VOLUME_LOC_NOT_FOUND by disabling gotd downloader verifier ([414faf3](https://github.com/thedavidweng/tg-drive/commit/414faf3e38d7ebfca224ff74dea3ea56500b469d))
* **telegramgotd:** stop dialog iteration once the target channel is resolved ([ce2e321](https://github.com/thedavidweng/tg-drive/commit/ce2e3214d0798e332bf36d9808473f943037b3d1))


### Refactoring

* extract deep modules and ports from the command layer ([36fba26](https://github.com/thedavidweng/tg-drive/commit/36fba26adfd68e50a1c5e94ccebf5e092ee9ae99))
* fix PR [#34](https://github.com/thedavidweng/tg-drive/issues/34) review findings ([#40](https://github.com/thedavidweng/tg-drive/issues/40)) ([1c49a46](https://github.com/thedavidweng/tg-drive/commit/1c49a46768a1f1f64c5be091ef171855a659550f))
* rename import to adopt; reserve import for external-chat ingest (ADR 0019) ([c0846c3](https://github.com/thedavidweng/tg-drive/commit/c0846c3ff6dbefacb51281a933ab7a5b79ecb3c9))


### Documentation

* add Diataxis user guides under docs/guides/ ([d3d717b](https://github.com/thedavidweng/tg-drive/commit/d3d717bcbc905f5d182339c03b7192af540e152d))
* drop stale plans and rewrite public docs for release ([4bcd7f4](https://github.com/thedavidweng/tg-drive/commit/4bcd7f49673b99dc9d0fff69af5d46740b785777))

## Changelog

Managed by release-please.
