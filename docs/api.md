# REST API and Home Assistant

JSON endpoints use optional management Basic authentication when `RETRO_ADMIN_PASSWORD` is set. Radio protocol and stream endpoints remain accessible to the radio. This version is primarily read-only; browser form mutations are not a stable REST API.

| Endpoint | Query | Response |
| --- | --- | --- |
| `GET /healthz` | — | Plain `ok`; DB reachability only; no auth |
| `GET /api/v1/devices` | — | Registered devices/capabilities/last seen |
| `GET /api/v1/stations` | `q` optional | Local station metadata; no upstream URLs |
| `GET /api/v1/favourites` | `device=<device-id>` | That device's favourites |
| `GET /api/v1/play` | `device=<device-id>&station=1001` | `{"url":"http://.../stream/<opaque-id>"}` for legacy profile |
| `GET /api/v1/streams` | — | Currently connected relays, station name and start time |
| `GET /api/v1/activity` | — | Latest 100 events, newest first |
| `GET /diagnostics` | — | Downloadable JSON; raw radio identifiers and IPs omitted |

`device-id` is the pseudonymous ID in the devices response, not the radio's raw `mac` parameter. IP addresses are informational and may change. Relay activity currently has station/session information but no device association: radio stream requests carry no directory identity. “Connected” is a relay socket state, not FSAPI playback feedback or confirmed audible sound.

## Home Assistant design

[Home Assistant's Frontier Silicon integration](https://www.home-assistant.io/integrations/frontier_silicon/) controls supported radios through local FSAPI. Retro Radio replaces the directory and stream transport; it does not duplicate FSAPI or require Home Assistant.

First generate an HTTP URL using the play endpoint and the target radio's device ID. Use the radio's existing media-player integration for selection **only if that radio/integration supports playing a supplied URL**. This milestone does not claim verified `media_player.play_media` support for the ELAN IR5: the FSAPI integration and firmware may only select an existing preset or navigation item. Test this ability before building an automation around it.

A straightforward supported fallback is to save the managed Smooth station as a radio preset while browsing Retro Radio, then use the Frontier Silicon integration's supported preset controls. This retains Retro Radio's relay and avoids duplicating device control. Future catalogue/preset helpers should use stable station and stream IDs. Health endpoints, station validation and optional MQTT can follow; there is no MQTT broker or Home Assistant runtime dependency in this version.

## Milestone 2 updates

- `GET /api/v1/stations` now returns saved catalogue provenance and adaptive flags. It searches only local saved stations. The stream URL itself remains omitted.
- `GET /api/v1/health?station=<local-id>` returns an on-demand health result, or 404 if no check exists. Results include check and last-success timestamps, status/type, final URL, TLS requirement, inferred codec/bitrate and audio-received status. A final URL may contain upstream credentials in its query; protect the management API and do not share it as a diagnostic export. Exported diagnostics continue to omit station URLs and raw radio identifiers.
- `GET /api/v1/play` applies recent stored health and the target radio's profile. Unsupported codec/adaptive streams return 422. Legacy HTTPS stations use HTTP relay URLs.
- The browser-facing routes `/stations/results`, `/stations/card`, `/stations/select`, `/devices/stations`, `/stations/check` and `/custom` are HTML fragment/form endpoints, not a versioned REST contract. Search terms go to Radio-Browser; device identifiers do not.
- Activity checks are on demand, and a connected stream is still not proof of audible playback. No scheduler/MQTT/FSAPI control dependency was added.
