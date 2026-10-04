# Architecture

## Components

- `cmd/retro-radio`: configuration, HTTP listener and CLI health check.
- `internal/protocol/frontierxml`: legacy directory requests, registration, favourites, search, metadata groups and station lookup.
- `internal/store`: SQLite persistence, migrations, stable identifiers, per-radio favourites, health records and bounded directory caches.
- `internal/catalogue`: Radio-Browser mirror discovery, filtering, failover and cached candidate resolution.
- `internal/podcast`: bounded RSS/Atom parsing, cached Apple search and hourly feed refresh.
- `internal/delivery`: checked upstream connections, live probes, HTTP relays and adaptive audio conversion.
- `internal/web`: authenticated management routes, Go templates, progressive discovery and bundled HTMX.

One Go process serves both the radio protocol and the management interface. The Docker image includes FFmpeg and runs as UID/GID 10001. SQLite is held in a persistent volume. The application listens on port 8080 by default; Compose exposes port 80 for legacy radios.

## Library and radios

All saved stations are available on every radio. Favourites belong to a radio/station pair. Older assignment records do not restrict shared-library visibility. Country and genre groups come from station metadata, with an uncategorised group for missing values.

Stations have numeric directory IDs and random opaque playback IDs. A monotonic counter prevents a deleted directory ID from being reused for a different station. Editing a custom station preserves identifiers and clears stale health when its listening link changes. Removal deletes its library entry, associated favourites and health in one transaction.

Registration hashes the opaque protocol identifier. An incoming request identifies an adapter namespace, not an exact model. Human-readable radio names are separate from protocol metadata.

## Discovery and management

Radio-Browser mirrors use forward/reverse DNS discovery with fallback hosts. Searches combine name, country and tag filters, with 24 results per page and bounded offsets. Query results and candidates are cached separately from the library, retaining at most 128 searches and 2,000 candidates. Offline results are labelled in plain language.

Popular discovery caches background jobs by country and distinct-channel offset, up to eight jobs with bounded lifetimes. Checks run in batches of four; usable results are progressively rendered through HTMX, retaining ranking and returning up to 24 stations per page. Pagination retains exact channel cursors so skipped unavailable streams do not create gaps when returning to earlier pages. Each library addition performs its own live check before saving; a discovery check does not bypass admission.

A single Go template renders station cards across discovery, search, library and radio views. Context determines Add, Remove or favourite controls. The persistent shell loads page fragments, preserves normal URLs and supports non-HTMX forms. Destructive library actions use styled confirmations; result dialogs report additions. Technical activity remains available in the support report.

## Playback and upstream safety

Playback accepts stored opaque identifiers rather than arbitrary target URLs. Initial links, DNS answers and redirects reject loopback, private, link-local, metadata and other non-public addresses. Checked addresses are pinned for dialing while TLS hostname verification stays enabled. URL credentials and environment HTTP proxies are disallowed. Public ports 80, 443 and 1024–65535 are supported.

HTTP links are attempted over verified HTTPS first, falling back to the original HTTP origin when necessary. Explicit HTTPS links retain certificate checks. Compatible, recently checked HTTP streams may be played directly; other legacy-radio streams use a local HTTP relay. Direct-play health expires after one hour. The default profile allows MP3 and AAC over HTTP, including ICY metadata.

Streaming uses bounded buffers and connection/header/idle/write limits. Range requests and HTTP/1.0 clients are supported. Raw `ICY 200 OK` status lines and PLS playlists are not supported.

HLS and DASH are decoded by FFmpeg into continuous 128 kbps MP3, 44.1 kHz stereo. FFmpeg reads a temporary token-protected loopback gateway; remote manifests and segments still pass through the checked transport. Manifests are limited to 2 MiB and individual media responses to 32 MiB. Encrypted HLS and protected DASH are rejected. Up to four conversion processes run concurrently and stop on cancellation or disconnect.

## Authentication and scope

An optional management password protects the web interface, REST API and support report. Browser writes reject cross-origin submissions. Radio endpoints remain unauthenticated for compatibility. Directory identifiers are spoofable and playback IDs are bearer URLs: this is a trusted-LAN service, not a multi-tenant public proxy. Remote management requires operator-provided network protection.

Audio checks record timestamps, connection metadata and last success without downloading a whole stream. They establish receipt of audio, not physical-radio decoding. Later failures do not remove saved stations.

## Channels and audio alternatives

Schema 6 stores stream alternatives, UUID aliases, old preset/stream aliases and per-radio audio preferences. Existing stations are consolidated only when normalized name, country and website or stream host establish a match; language and programme distinctions are retained. Custom stations remain independent. Favourite membership is unioned when channels merge. The migration is transactional.

Catalogue pages assemble a bounded prefix of cached directory pages and group before applying UI offsets, so pages contain 24 distinct channels. Library admission probes up to 16 compatible alternatives, four at a time within 90 seconds, and saves only working playable streams. Playback checks capabilities, recent failures, direct/adaptive delivery and estimated codec/bitrate quality. Multi-stream channels always use the relay so startup failures can fall back before headers/audio are committed. Once playback starts, a change of codec requires reconnecting. Radio-specific overrides change the first attempted stream, retaining fallback.

## Podcasts

Schema 7 adds shared subscriptions and episode records without changing station or favourite data. A refresh atomically updates metadata and matches episodes by publisher GUID (falling back to enclosure URL), preserving IDs. It accepts up to 200 public MP3/AAC episodes per feed and retains at most 500 recent episodes. Feed failures preserve existing data. Deleting a subscription cascades to its episodes; a refresh cannot recreate a removed subscription.

Feeds have an 8 MiB limit and a 20-second deadline, and use the same DNS-pinned, public-address-only client as streams. XML external entities are not expanded. Audio enclosure URLs are validated before saving and checked again through the playback transport. Podcast search uses Apple’s public search endpoint with 24 results, a bounded 15-minute cache and a 15-second deadline. No remote artwork is loaded. Episode playback uses the existing bounded relay, HTTP/HTTPS negotiation, GET/HEAD and Range support. `/episode/<id>` accepts stored identifiers only, and is accessible to radios without management authentication. Subscription and episode IDs can be guessed; this remains a trusted-LAN service.
