# Development status

Updated 5 October 2026.

## MinimServer milestone

Implemented the server-side Music → MinimServer → native server hierarchy → track lookup → playback path using a general Browse/Resolve content provider. Automatic SSDP discovery connects music servers without user-supplied URLs or ports. An optional manual device-description URL remains an administrator fallback. Linux Compose includes a host-network discovery helper while preserving the application bridge/HTTP mapping. Paginated SOAP/DIDL browsing, native MP3/AAC resource selection followed by bounded FFmpeg FLAC → MP3 fallback, explicit HTTPS radio capability, opaque scoped LAN audio/artwork endpoints, metadata and 128-pixel JPEG logos are implemented. The authenticated Music browser shows discovered servers automatically as clickable folders, with simple Back navigation and track controls. Discovery/setup buttons and format explanations are absent from normal browsing; the dashboard uses the shared Music menu. Music pagination stays within the current folder, and the compact header fits narrow iPhone screens. Music station selection now uses numeric IDs and the established Radio format, with bounded opaque-token aliases and lookup diagnostics; apostrophes and quotation marks are emitted as literal XML text for legacy displays; the owner has now confirmed native MP3 playback and FLAC → MP3 fallback playback on the physical Pure. No music files are scanned and no library is mirrored to SQLite.

The owner confirmed the menu-to-playback path on physical Pure hardware, including native MP3 and the FLAC-only “Chan Chan” converted to MP3. Automated fixtures cover menu traversal through Album/album/track, lookup metadata/artwork, page mappings, resource alternatives, malformed/offline servers, scoped transport/redirects, invalid/expired opaque IDs, byte-range/HEAD playback, artwork and web authentication/failure isolation. The owner reports Pure HTTPS support; this remains an explicit operator setting rather than an automatically detected capability.

FLAC-only CD tracks use the automatic MP3 fallback when FFmpeg is available (already included in Docker). Native compatible resources take priority. MinimStreamer's documented local FLAC output formats were investigated first and do not guarantee an MP3 resource. Music conversion tests cover native preference, disabled/missing FFmpeg, lifecycle/slot cleanup and range/HEAD behaviour; generated real-audio encoding tests run when FFmpeg is available. Physical Pure display of track metadata/artwork remains acceptance work; audible native MP3 and converted FLAC playback is confirmed. Music presets/favourites, whole-album auto-advance and seeking in converted tracks are deferred.

## Implemented

- Frontier XML directory adapter with stable station and stream identifiers.
- SQLite migrations preserving devices, stations and individual favourites.
- A shared library available to all radios, with country and genre menus.
- Conservative channel grouping, multiple checked audio alternatives, startup fallback and per-radio audio preferences.
- Radio-Browser discovery, 24-result search pagination, combined filters and bounded offline caching.
- Progressive discovery of 24 usable popular stations per page in the selected country, with pagination above and below discovery, search and library lists.
- Custom station creation and editing with automatic audio detection and checks before saving.
- HTTP/HTTPS negotiation and checked audio relays, plus FFmpeg conversion of HLS/DASH to MP3.
- Podcast discovery, shared RSS/Atom subscriptions, hourly refresh, stable episode IDs, browser previews and Frontier on-demand menus.
- An HTMX interface with Dashboard, Stations, Podcasts, Radios, Activity and Help; plain-language feedback and styled confirmations.
- Docker deployment with a persistent data volume and management authentication.

## Verification

Automatic discovery coverage includes no-configuration connection, multiple advertisement identity handling, source-bound description validation, playback/artwork routing, background refresh/cancellation, offline retention/expiry, manual fallback and fresh/stale/malformed helper handoffs. A native scan on the development Mac found Music Library and Evo One, while the same code in an isolated bridge container found no servers, confirming the Docker discovery boundary. The Linux helper is intended to put discovery on the host LAN; physical acceptance on the target Linux/NAS remains required.


The owner confirmed audible Smooth Radio playback on a physical Pure ELAN IR5 on 3 October 2026, and subsequently confirmed AAC playback. These are hardware observations, not automatic identification of every registered radio.

Automated coverage includes protocol fixtures, database migration and stable identifiers, shared-library and favourite behaviour, directory failover/cache, unsafe URL rejection, streaming, audio probes, adaptive conversion, authentication and web journeys. Synthetic fixtures use fake radio identifiers.

Local Go tests, race checks, vet and Linux amd64/arm64 builds have passed during development. Generated HLS/DASH conversion tests run when FFmpeg is installed. Live BBC HLS audio has been received and converted by the server; no physical-radio BBC result is claimed.

The Docker image has been built and the local service upgraded repeatedly while preserving its volume. Browser walkthroughs covered navigation, discovery and filtering, library management, radio favourites, custom forms, listening activity, help, confirmation cancellation and unsuccessful additions. Synthetic save/delete tests use disposable databases. BBC “More or Less” discovery, subscription, episode browsing and browser audio delivery were verified on an isolated server. Podcast radio menus, persistence, feed failures, unsafe links and byte-range playback have automated coverage.

## Still open

- Physical Pure MinimServer acceptance, including real server description/resource URLs, track details/artwork, seeking and HTTPS delivery.
- Physical acceptance of the FLAC → MP3 fallback; seeking in converted tracks remains unsupported.
- Persistent music favourites/presets and album auto-advance.

- Broader physical-radio acceptance: navigation, reconnects, power-cycle persistence and additional models.
- Redacted complete Pure protocol captures; existing fixtures remain reference-derived.
- Physical-radio podcast acceptance, M4A/MP4 podcast support and listening-position sync.
- Scheduled background station health checks and additional directory-protocol adapters.
- Physical-radio acceptance of all adaptive-stream variants and broadcaster geographic restrictions.

Streams and public directory entries can change. A passing server probe is not evidence of audible playback on every radio.

The Pure top level now groups Radio, Podcasts and Music, retaining all radio choices within Radio and existing endpoint compatibility. Music cards show title, artist, album and duration beside the cover, with full-width native audio controls matching station and podcast players.

Nested web Back links now precede the heading, and music uses the current folder title as its page heading. Browser verification covers album navigation, section changes, pagination, explicit Back, browser Back/Forward, background refreshes and in-place saves. Album covers are 25% larger and verified at 320, 375, 390, 768 and 1280 pixel widths.

Automatic discovery now hides empty ContentDirectory libraries rather than showing players as music sources. Live probing found Evo One had zero root entries while Music Library had eight; the revised manager lists only Music Library. Mocked tests cover empty-source filtering, later content availability, and temporary browse failures without losing a working library.
