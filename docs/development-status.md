# Development status

Updated 4 October 2026.

## Implemented

- Frontier XML directory adapter with stable station and stream identifiers.
- SQLite migrations preserving devices, stations and individual favourites.
- A shared library available to all radios, with country and genre menus.
- Radio-Browser discovery, 24-result search pagination, combined filters and bounded offline caching.
- Progressive discovery of up to 12 usable popular stations in the selected country.
- Custom station creation and editing with automatic audio detection and checks before saving.
- HTTP/HTTPS negotiation and checked audio relays, plus FFmpeg conversion of HLS/DASH to MP3.
- An HTMX interface with Dashboard, Stations, Radios, Activity and Help; plain-language feedback and styled confirmations.
- Docker deployment with a persistent data volume and management authentication.

## Verification

The owner confirmed audible Smooth Radio playback on a physical Pure ELAN IR5 on 3 October 2026, and subsequently confirmed AAC playback. These are hardware observations, not automatic identification of every registered radio.

Automated coverage includes protocol fixtures, database migration and stable identifiers, shared-library and favourite behaviour, directory failover/cache, unsafe URL rejection, streaming, audio probes, adaptive conversion, authentication and web journeys. Synthetic fixtures use fake radio identifiers.

Local Go tests, race checks, vet and Linux amd64/arm64 builds have passed during development. Generated HLS/DASH conversion tests run when FFmpeg is installed. Live BBC HLS audio has been received and converted by the server; no physical-radio BBC result is claimed.

The Docker image has been built and the local service upgraded repeatedly while preserving its volume. Browser walkthroughs covered navigation, discovery and filtering, library management, radio favourites, custom forms, listening activity, help, confirmation cancellation and unsuccessful additions. Synthetic save/delete tests use disposable databases.

## Still open

- Broader physical-radio acceptance: navigation, reconnects, power-cycle persistence and additional models.
- Redacted complete Pure protocol captures; existing fixtures remain reference-derived.
- Podcasts, scheduled background health checks and additional directory-protocol adapters.
- Physical-radio acceptance of all adaptive-stream variants and broadcaster geographic restrictions.

Streams and public directory entries can change. A passing server probe is not evidence of audible playback on every radio.
