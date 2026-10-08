# Retro Radio Server

A local audio-content hub and compatibility layer that keeps legacy internet radios playing. Discover stations, build a shared library and choose favourites for each radio through a neon-themed web interface.

Built with Go, SQLite and server-rendered HTMX. Runs in one Docker container, including FFmpeg for modern adaptive streams. Licensed under GPL-3.0-only.

## Features

- Radio-Browser discovery, name search, country and genre filters.
- Live-checked popular stations in your selected country, loaded progressively with 24 stations per page. Search and library lists also use 24 stations per page.
- One channel per station, with conservatively matched stream alternatives. Automatic audio selection and startup fallback, plus optional audio choices per radio.
- A shared station library available to every radio, with individual favourites.
- Custom stations from public listening links, with automatic audio detection.
- Radio menus for all stations, country, genre, favourites, podcasts and search.
- My Music menus from automatically discovered MinimServer / UPnP ContentDirectory servers, with paginated browsing, track details, album artwork and opaque playback links.
- Podcast search, shared RSS/Atom subscriptions and hourly episode updates, with MP3/AAC playback and browser previews.
- Automatic HTTP/HTTPS negotiation and local HTTP delivery for older radios.
- HLS and MPEG-DASH audio converted to continuous MP3, including BBC-style streams.
- A responsive web interface with a dashboard, Stations, Podcasts, Radios, Activity and Help.
- Persistent stations, favourites, radio names and stable station identifiers.

## Hardware status

MP3 and AAC playback have been confirmed by the owner on a physical **Pure ELAN IR5**, including Smooth Radio. This does not establish compatibility with every Frontier radio or every stream. Detailed protocol captures and broader hardware acceptance remain open.

The current adapter implements the Frontier XML directory protocol. Other Frontier XML radios are experimental; Frontier JSON, vTuner and Reciva adapters are not implemented. See the [hardware checklist](docs/device-compatibility.md).

## Quick start

You need Docker Compose, a computer on your home network and a DNS resolver that supports local hostname overrides.

```sh
git clone https://github.com/paulbowler/retro-radio-server.git
cd retro-radio-server
cp .env.example .env
```

Set `RETRO_ADMIN_PASSWORD` in `.env` to a password of your choice, then start:

```sh
docker compose up -d --build
```

Open `http://<server-LAN-IP>/` and sign in with `admin` and your password. Compose exposes port **80**, which the radio requires.

Add this local DNS override and make sure the radio uses that resolver:

```text
pure.wifiradiofrontier.com → <server-LAN-IP>
```

Open Internet Radio on the radio. It should appear under **Radios**. Only override the directory hostname; broadcaster hostnames must resolve normally. See [DNS setup](docs/dns-setup.md) for examples.

### Find stations and choose favourites

1. Open **Stations → Discover**. Browse popular stations or combine name, country and genre filters.
2. Use a station’s player to listen in your browser, then choose **Add to library**. The server must receive playable audio before saving the station; a popup reports success or failure.
3. Every saved station is available on all current and future radios.
4. Open **Radios → Manage favourites** and use the hearts to choose favourites for that radio.
5. Use the radio’s **All stations**, **By country**, **By genre** or **Favourites** menus to listen.

Use **Stations → Your library** to manage saved stations. **Remove** deletes a station from every radio and its favourites after confirmation. **Add custom station** accepts a listening link, with optional country and genre metadata.

Country suggestions use browser regional settings, with a remembered manual choice. Search terms and filters are sent to Radio-Browser; radio identifiers are not. No votes or playback-click telemetry are sent. Cached directory results remain usable during outages, independently of your saved library.

### Listen to podcasts

1. Open **Podcasts**, search by name and choose **Add podcast**. You stay on the list with your search intact, ready to add another. Alternatively, choose **Add by feed link** and paste a public podcast RSS or Atom link.
2. Open **Podcasts** in your radio’s Internet Radio menu, choose a show and then an episode. Refresh the radio’s directory if the new menu is not visible yet.
3. The web interface also lets you browse episodes and listen in your browser. Lists show 24 episodes per page, newest first.

Subscriptions are shared by all radios. New episodes are fetched hourly; a failed feed refresh preserves the last successful episode list. Each refresh reads up to 200 compatible episodes, with up to 500 recent episodes retained per subscription. Removing a podcast removes its subscription and episode links from every radio.

Search uses the [Apple podcast catalogue](https://developer.apple.com/library/archive/documentation/AudioVideo/Conceptual/iTuneSearchAPI/Searching.html), without an account or API key. Search terms and a regional country code go to Apple; radio identifiers do not. Public RSS/Atom feeds with MP3 or AAC enclosures are supported. M4A/MP4, video, paid/authenticated feeds and listening-position sync are not supported yet. Audio passes through the checked HTTP relay, including HTTPS negotiation and byte-range requests where the publisher supports them. Podcast menu responses are tested against the documented Frontier format; physical-radio podcast playback still needs confirmation.

### Browse MinimServer music

MinimServer remains the authoritative library: it scans the music, supplies metadata and organisation, indexes/searches it and serves source audio. Retro Radio does not scan your NAS, parse music files or mirror the library into SQLite.

1. Start or update Retro Radio with `docker compose up -d --build`. On Linux/NAS, Compose starts a small LAN discovery helper alongside the application. It uses the host network for SSDP and passes discovered server descriptions through a shared, read-only handoff to the application. The application keeps its existing port mapping and database volume.
2. With MinimServer running on the same LAN, open **Music** in the web menu. Music servers appear automatically by their published names, and the server list refreshes while you view it. Select a server to browse its folders and play tracks; no addresses, ports or description paths need entering.
3. After an update or server restart, reopen the music menu so playback links are fresh. Refresh the Pure directory, then choose **Music → your MinimServer → Album → an album → a track**. The folders underneath the server come from MinimServer, so their names and structure may differ. Multiple discovered UPnP music servers can coexist under Music.

Discovery starts in the background and repeats every 30 seconds. Brief missed announcements retain a server; one absent for five minutes is removed automatically. Radio/podcast service never waits for discovery. The helper discovers MediaServer and ContentDirectory advertisements on active multicast-capable IPv4 interfaces, avoiding dependence on one default route. Source addresses and device descriptions are checked before a server is added. Automatic discovery also requests one root entry: devices with an empty library (including players advertising an unused ContentDirectory) are hidden and checked again on subsequent scans. They appear when content becomes available. Temporary browse failures retain an existing source for the usual grace period; explicitly configured manual sources remain visible.

The Linux discovery helper solves the bridge-network boundary without changing the radio HTTP listener. Docker Desktop uses a VM and does not offer the same LAN multicast reachability: for Mac/Windows development, running the native server on the LAN provides discovery; the helper alone does not promise Docker Desktop multicast support. VLANs or client isolation can also block discovery. These are deployment/network concerns rather than information a music listener should have to enter.

<details><summary>Advanced manual fallback for isolated networks</summary>

An administrator may set `RETRO_MUSIC_SERVER_URL` to the exact UPnP device-description URL if the server is intentionally on a network where discovery cannot reach it. That server remains available alongside automatic discovery. This is optional; no music URL is required for ordinary Linux/NAS deployments. The description URL is a different endpoint from the MinimServer settings web page.

</details>

Compatible MP3 and ADTS AAC resources are preferred. When MinimServer offers only FLAC, FFmpeg converts it on demand to MP3 at 128 kbps, 44.1 kHz, stereo. Radio music playback uses the local opaque relay, including compatible native HTTPS resources, so the dashboard can attribute playback to the right radio. Native MP3/AAC audio remains unchanged. Docker already includes FFmpeg; native installs need `ffmpeg` with `libmp3lame` on `PATH`. Set `RETRO_MUSIC_TRANSCODE=false` to disable the fallback. Set `RETRO_RADIO_HTTPS=true` only if HTTPS playback is known to work on **all radios** using this directory. It records the HTTPS capability in the directory profile; music still uses the relay for playback tracking. The default remains false. This is an operator setting, not automatic model detection. Internal DNS names such as `music.home.paulbowler.co.uk` work for configured HTTPS description/resource URLs, with normal certificate verification. DNS alone does not supply a UPnP description path. `RETRO_PUBLIC_URL` remains the existing HTTP radio directory/relay origin; an HTTPS management address such as `https://radio.home.paulbowler.co.uk` can still be supplied by your reverse proxy.

All server-fetched resources and artwork must be on the configured server hostname or one of its pinned IP addresses. Different ports on that same host are allowed (80, 443, or 1024–65535). If a reverse proxy publishes a description on one hostname but MinimServer advertises resources on a different hostname/address, the provider rejects them unless that address is the same pinned server IP. Configure the description and advertised resources consistently; this intentionally does not open access to arbitrary LAN hosts. Automatically discovered endpoints are refreshed when their advertised address or location changes. A manually configured server retains its pinning until Retro Radio restarts.

Native audio delivery forwards GET/HEAD and byte ranges without changing source audio. Converted FLAC starts at the beginning and does not support seeking; an initial `Range: bytes=0-` request restarts playback, while other ranges are rejected. Conversion is limited to four concurrent tracks and ends on disconnect, cancellation or source failure. Unknown and expired playback/artwork IDs cannot select arbitrary URLs. Artist, album and duration are included in the established description field; album art is delivered as the same bounded 128-pixel JPEG used for station logos. Actual display fields depend on the Pure firmware and need hardware acceptance. Music tokens are held in bounded memory, expire after 24 hours without reuse, and reset on restart; browse again to refresh links. Music favourites/presets, search and a mirrored music database are not part of this milestone. Lowest-level track folders now offer **[Play All]** on the Pure, using one continuous stream with changing ICY track metadata. Selecting a track continues through the rest of that folder. See [Play All deployment and testing](docs/play-all.md).

FLAC conversion consumes only the bytes MinimServer serves, with no NAS filesystem access, persistent converted files or metadata parsing from the audio. FFmpeg has no source URL and is restricted to a FLAC input pipe and MP3 output pipe. [MinimStreamer's documented transcoding](https://minimstreamer.com/userguide.html) was investigated first; its documented local FLAC outputs are PCM/WAV rather than a compatible MP3 resource, so this fallback closes that gap. Native resources still take priority, including any compatible resource already provided by MinimServer. WAV/FLAC direct radio decoding is not assumed. The first audible physical-Pure music test, including metadata/artwork display, remains unverified.

SSDP searches last at most two seconds; server validation runs in a separate, bounded background refresh. The Linux host-network helper opens no HTTP listener and needs no elevated capabilities. Its handoff contains only transient discovery information, expires after 90 seconds and never contains the music library. Music requests time out independently and an unavailable server does not prevent radio/podcast browsing or server startup.

### Restart or upgrade

```sh
docker compose up -d --build
```

If a build stops with `compile: signal: killed`, the compiler may have run out of memory. The Docker build compiles packages one at a time and uses more frequent garbage collection to reduce its memory demand. Pull the latest code and retry `docker compose up -d --build`. If it still fails, check the host’s out-of-memory logs and increase the memory available to Docker or build on a larger machine. These settings apply only while building; the running server uses its normal settings.

The database lives in a persistent Docker volume. Keep that volume when rebuilding; `docker compose down -v` deletes its data. Back up SQLite with the service stopped or through SQLite’s backup API. For a Linux bind mount instead of the named volume, the data directory must be writable by UID/GID `10001:10001`.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `RETRO_LISTEN` | `:8080` | Application HTTP listener; Compose maps port 80 to it |
| `RETRO_PUBLIC_URL` | `http://pure.wifiradiofrontier.com` | HTTP origin reachable by the radios, without path or query |
| `RETRO_DB` | `data/retro-radio.db` | Database path; container uses `/data/retro-radio.db` |
| `RETRO_ADMIN_USER` | `admin` | Management username |
| `RETRO_ADMIN_PASSWORD` | empty | Enables authentication for the web interface, REST API and support report |
| `RETRO_CATALOGUE_URL` | DNS-discovered mirrors | Optional public Radio-Browser API origin |
| `RETRO_MUSIC_SERVER_URL` | empty | Optional administrator fallback; normal operation discovers music servers automatically |
| `RETRO_UPNP_DISCOVERY_FILE` | empty natively; set by Compose | Internal handoff from the Linux LAN discovery helper; configured automatically |
| `RETRO_MUSIC_TRANSCODE` | `true` | FLAC → MP3 fallback when native compatible resources are absent and FFmpeg is available |
| `RETRO_RADIO_HTTPS` | `false` | Operator-confirmed HTTPS capability for every radio; music uses the relay for dashboard tracking |
| `RETRO_HEALTH_URL` | `http://127.0.0.1:8080/healthz` | CLI health check URL |

This is a **trusted home-network service**. Management Basic authentication uses HTTP unless you provide a TLS terminator or VPN. Radio endpoints cannot require management credentials. Keep the radio’s HTTP access available and do not expose the service as a public internet proxy. Local `.env` files, databases and backups are excluded from Git.

## Add Retro Radio to your Home Screen

After updating the server, open its HTTPS web address in Safari on iPhone or iPad, choose Share → Add to Home Screen, then Add. The suggested name is **Retro Radio**, with the neon radio logo. Enable **Open as Web App** if Safari offers that option. Android and desktop browsers can use their Install app or Add to Home Screen option where available.

The installed app opens on the dashboard in its own window. Existing Home Screen shortcuts may keep their old name or icon; remove and add the shortcut again to refresh them. The app still needs a connection to your Retro Radio server and the broadcaster. Only public app branding and installation metadata are available without management credentials; management pages keep their existing login settings.

## App settings

The cog in the header opens shared settings for every radio. The logo returns to the dashboard. On the Pure, the top-level choices are Radio, Podcasts and Music. Radio contains All stations, By country, By genre, Favourites and Search stations. Web music cards place track details beside the artwork and use full-width native audio controls, matching the other players.

- **Playback buffer:** 0–10 seconds of live audio held ahead of playback. Larger values add tuning delay and can smooth short connection drops. The duration is estimated from the station’s bitrate, so variable or unknown bitrates can differ. Zero keeps immediate playback.
- **Automatically reconnect:** retries the same live stream after a disconnect or stall, keeping the radio connection open for up to roughly a minute of retries. A buffer can cover short interruptions; longer outages can still pause playback. Reconnecting joins the current live broadcast and may skip content missed during the outage.
- **Audio quality:** Auto chooses the usual compatible stream; Lower data use favours a lower known bitrate when alternatives are available. Availability and compatibility still take priority.
- **Default country:** sets the starting country for discovery. Explicit search filters take precedence; Automatic uses the browser’s regional settings.

Preferences are stored in the database and apply to new listening sessions. Defaults retain immediate playback, automatic quality and automatic country; reconnection starts off. Live MP3/AAC and converted HLS/DASH use the buffer. Podcast downloads and byte-range requests retain normal delivery and seeking. Buffer memory is bounded per listener, and song information is delivered alongside the buffered audio.

## Audio delivery

Direct MP3 and AAC streams can play on the default radio profile. HTTPS audio is delivered through a local HTTP relay when necessary. HTTP listening links are tried over verified HTTPS first, with fallback to the original HTTP origin when HTTPS is unavailable. Broadcaster-specific rules are not required.

HLS and MPEG-DASH audio, including AAC-LC and HE-AAC, is converted by FFmpeg to MP3 at 128 kbps, 44.1 kHz, stereo. Docker includes FFmpeg; native installations need it on `PATH` with an MP3 encoder. Four conversion processes can run concurrently. Protected/DRM streams and PLS playlists are unsupported. Availability and geographic restrictions depend on the broadcaster.

Radio and podcast connections, redirects, manifests and segments reject non-public upstream addresses, pin checked DNS addresses and retain TLS verification. Stored stream identifiers are used instead of accepting arbitrary playback URLs. See [architecture](docs/architecture.md) for limits and implementation details.

A stream check confirms audio can be received; it cannot prove a physical radio will decode every station. Later failed checks do not delete saved stations.

## Native development

Use Go 1.23 or later and FFmpeg for adaptive-stream tests:

```sh
go test -race ./...
go vet ./...
go build -o retro-radio ./cmd/retro-radio
RETRO_PUBLIC_URL=http://<server-LAN-IP> ./retro-radio
```

The native server listens on port 8080 by default. Factory radios require port 80; use Docker, port forwarding or `RETRO_LISTEN=:80` with appropriate host privileges.

The web interface uses Go templates and bundled HTMX, with no Node build or frontend framework. Ordinary links, forms and bookmarked URLs remain available. GitHub Actions runs tests, vet and Linux amd64/arm64 builds. Generated adaptive-stream tests need FFmpeg; optional live BBC verification uses `RETRO_TEST_BBC_URL`.

## Documentation and contributions

- [Development status](docs/development-status.md)
- [Architecture](docs/architecture.md)
- [Frontier protocol research](docs/frontier-protocol.md)
- [Hardware acceptance checklist](docs/device-compatibility.md)
- [DNS setup](docs/dns-setup.md)
- [REST API and optional Home Assistant integration](docs/api.md)

Issues and pull requests are welcome. For hardware reports, include the model, firmware and steps to reproduce. Remove radio identifiers, passwords and private configuration from traces. Broader radio coverage and scheduled health checks are possible future work.

## Licence and acknowledgements

GPL-3.0-only; see [LICENSE](LICENSE). Bundled HTMX retains its [BSD licence](internal/web/static/HTMX-LICENSE). Go dependencies and checksums are recorded in `go.mod` and `go.sum`.

KIMB Radio-API, WiFi-RadioAPI, LibreFrontier, YCast and YTuner were studied for interoperability behaviour without porting their source or adding runtime dependencies. Radio-Browser integration follows its [official API documentation](https://docs.radio-browser.info/).

Audio alternatives are grouped when the station name and country match and the website or stream host supports the match. Technical codec/bitrate suffixes are removed from names; programme and regional names remain distinct. Automatic playback prefers compatible direct streams and estimates quality from codec and bitrate, placing recently failed streams last. Channels with alternatives use a stable server playback address and try another stream if connection, status or initial audio fails. A format change requires a new playback connection once audio has started. Audio options on a radio let you override the first choice; fallback still applies. Existing library stations acquire newly discovered working alternatives during their automatic checks.

Database schema 6 preserves alternatives and combines favourites for confirmed duplicates. Old station and stream addresses remain aliases. Back up the persistent `/data` directory before upgrading.

Saved stations and per-radio favourites load from the local database without contacting Radio-Browser or probing streams when viewed. Artwork is cached persistently (including the radio-sized JPEG), shared across search and library cards, and served immediately. Images older than seven days refresh in the background; the last good image remains available during outages. Failed or missing artwork is retried after 15 minutes. The artwork cache is bounded to 64 MiB and survives server restarts. Directory searches retain their existing 15–30 minute cache and offline fallback.

Nested web pages place their Back link above the title. Opening a folder, switching sections or changing pages starts at the top; Back and Forward restore the previous scroll position. Background refreshes and in-place edits preserve it. Music album artwork is 25% larger while retaining the side-by-side track details.

Dashboard radio cards show station, podcast or music playback through Retro Radio. Music includes artist/album details and available artwork; podcasts show the episode and show title. Completed downloads remain visible for the supplied track/episode duration because the radio may still be playing buffered audio. This is an estimate: a local pause/stop after buffering is not observable, and unknown-duration media is shown only while its stream is connected. Web playback is excluded.

Music and podcast media clients carrying a valid radio ID remain attributable even if they use a browser-style user agent. Web players explicitly opt out with `listener=web`. Music stream activity records whether a radio was linked and whether the transfer completed, to help diagnose missing dashboard status.

Music status also retains the known-duration estimate when a radio releases a media connection while playing buffered audio. A client disconnect is not proof that the radio stopped; source failures still clear their own playback record.

Dashboard playing cards show artwork and station, programme or track details. Background checks update the cards only when their visible content changes.

### Agent FM

Genre folders also offer stations such as `[Jazz FM]`, which select music only
from that genre's NAS view. The top-level `Agent FM` still uses the whole library.

Enable the optional `Agent FM` station on the Pure's top-level menu to let an
online AI choose music from your UPnP servers and prepare natural spoken links
between tracks. Set `RETRO_AGENT_FM=true` and `OPENAI_API_KEY` in the server's
`.env`, then rebuild. See [Agent FM setup and radio testing](docs/agent-fm.md)
for voice settings, costs, fallback behaviour and the initial library limits.

## Development database rebuild

For schema/ingestion experiments, set `RETRO_DEVELOPMENT=true`, restart, and open
the settings cog → **Development: rebuild database…**. This destructive operation
stops listening and deletes saved development library/radio data, builds a fresh
database through current migrations, and refreshes normal sources. App settings,
secrets and feed subscriptions remain; a private backup is kept. See
[the rebuild guide](docs/development-rebuild.md) for exact scope, recovery and the
playback/artwork findings. Leave the feature disabled in production.
