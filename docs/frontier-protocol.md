# Frontier XML research and implemented transaction

## Evidence and confidence

The supplied ELAN IR5 capture establishes **only** `GET /setupapp/pure/asp/BrowseXML/loginXML.asp?token=0`, Host `pure.wifiradiofrontier.com`, User-Agent `FSL IR/0.1`. The supplied brief reports successful HTTP relaying of HTTPS audio, including Smooth, but contains no complete Pure directory capture. This document therefore distinguishes the known Pure request from a reference-derived candidate sequence. The owner confirmed Smooth playback on a physical Pure ELAN IR5 on 3 October 2026. A claim of an exact, verified ELAN IR5 wire transaction still requires a redacted hardware capture.

References studied on 3 October 2026:

| Project | Revision | Observations used |
| --- | --- | --- |
| [KIMB Radio-API](https://github.com/KIMB-technologies/Radio-API) | `6e0e0b3dc2c4324c453be02e4a51bc6330aca635` | `php/index.php`, `Auth.php`, `OutputXML.php`, `Output.php`, `Router.php`: challenge, opaque identity, full station output, explicit Content-Length, parameter-driven lookup; older XML/newer JSON separation |
| [WiFi-RadioAPI](https://github.com/kimbtech/WiFi-RadioAPI/blob/master/HamaAPI.md) | `d56a173f339f507af5ab37152eca2416359b7325` | Hama DIR3100MS documentation and connection log; login/root, item fields, case variants, pagination and favourites |
| [LibreFrontier](https://github.com/compujuckel/librefrontier) | `0f9c5404303848a07675e2447e993593452bf0b5` | Go XML structs, station lookup and search menu; model/protocol separation |
| [YCast](https://github.com/milaq/YCast) | `f349a2686c336420b8e2dc4c9a2a9fc12dd4e358` | XML menus, bookmark/search fields; a query delimiter is essential because radios blindly append `&mac=...`; geared to vTuner |
| [YTuner](https://github.com/coffeegreg/YTuner) | `ea7bfb3c54cc815b2ee5043984ba47fef75af3da` | vTuner device/DNS model, optional DNS proxy and Radio-Browser caching; MIT licence |

KIMB and LibreFrontier are GPL-3.0; YCast is GPL-3.0-or-later. This new Go implementation uses GPL-3.0-only. Source was read to understand behaviour, not copied/ported. The fixed challenge and protocol field names are interoperability facts. Reference repositories are research scratch material and are not included in the deliverable.

## Candidate Pure sequence implemented here

1. **Observed Pure request**: `GET /setupapp/pure/asp/BrowseXML/loginXML.asp?token=0`.
   Response body is exactly `<EncryptedToken>3a3f5ac48a1dab4e</EncryptedToken>`, no XML declaration. The value is supported by KIMB and Hama traces. It is not a management authentication credential.
2. **Reference-derived**: `GET .../loginXML.asp?gofile=&mac=<opaque>&dlang=eng&fver=8&ven=pure`.
   Register/update a device using the opaque identifier; do not interpret `mac` as a NIC MAC. Return `ListOfItems`, count 3, Previous plus My stations, Favourites and Search. Root has a Previous entry pointing to itself.
3. Follow advertised `.../navXML.asp?gofile=Radio`; the radio appends identity/language/firmware. Return count 1, Previous plus the short Station item (type, `StationId=1001`, `StationName=Smooth Radio`). Every navigation URL already includes `?`, allowing appended `&mac=...`.
4. **Reference-derived station selection**: `GET .../Search.asp?sSearchtype=3&Search=1001&mac=<opaque>`. Return Previous plus one full Station item with name, ID, HTTP `StationUrl`, description, empty Logo, Radio format, United Kingdom location, configured 128 kbps, MP3 mime and reliability 5.
5. Request `http://<configured-origin>/stream/<random-id>`, without radio identity parameters. The server opens `https://media-ice.musicradio.com/SmoothUKMP3` and relays audio unchanged. No 3xx Location or HTTPS URL is sent downstream.

All XML responses have explicit **byte** Content-Length and no-store. Manufacturer path matching is case insensitive; advertised paths preserve the incoming manufacturer. URLs do not derive from untrusted Host headers. The default origin is the same overridden Pure hostname. `RETRO_PUBLIC_URL` can instead be a LAN IP origin.

`ItemCount` excludes Previous and reports the full list length even on paginated responses. `startItems`/`endItems` are one-based inclusive. Unknown station lookups return 404; unknown endpoints return 404; invalid/missing identity returns 401. Requests without an identity to login return the challenge, matching KIMB's broad startup handling.

## Search and favourites

The root advertises a Search item with `SearchURL` ending `?sSearchtype=1` and the documented SearchButtonGo/Cancel/Textbox fields. Submitted `Search` is matched against the local station name. Search-type differences exist in prior art (LibreFrontier uses type 2); non-type-3 requests here use the local search path. The exact Pure search submission requires confirmation.

`FavXML.asp` and `AFavXML.asp` return this radio's stored favourites. The UI provides reliable add/remove actions. `AddFav.asp` and `RemoveFavs.asp` accept station ID variants (`ID`, `StationId`, `stationid`, `Search`), but these mutation parameter variants are **provisional**, not verified Pure captures. Radio-side bookmarking is not part of milestone acceptance. Podcast bookmarking and global favourites groups are not implemented.

## Remaining hardware uncertainties

The Hama documentation warns that some firmware mishandles XML entities, whereas YCast/LibreFrontier use standard XML escaping. This server emits valid XML; milestone URLs deliberately need only a single query parameter, avoiding `&amp;` in advertised navigation URLs. Future compound query URLs and names containing `&` need Pure verification before expanding the catalogue. Textbox and Logo are emitted as empty elements in the full shapes.

The existing Smooth URL from original presets may bypass directory lookup; the user must select the new station. Raw `ICY 200 OK`, adaptive playlists and codec conversion are deferred. Actual Smooth playback is now owner-confirmed on the Pure. Token stability, detailed startup challenge capture, Previous behaviour, firmware-specific search fields and paginated count handling remain on the hardware acceptance checklist.

## Fixtures

`internal/protocol/frontierxml/testdata/requests.json` contains representative synthetic requests using a fake token. They are derived from the supplied request plus public Hama documentation; **not a captured Pure session**. Tests assert fixed token, XML parseability, field semantics, explicit length, case variations, pagination, HTTP-only station URLs, device persistence and token-safe activity. Add redacted real Pure fixtures after the trial; never publish the live identifier.

## Milestone 2 catalogue behaviour

The established challenge is unchanged; the root now includes a Podcasts directory. Saved Radio-Browser/custom stations now share the normalised catalogue. Imported upstream UUIDs map to stable, short numeric Frontier IDs. Favourites remain per radio and persist across migration/restart. Directory responses include only codecs/adaptive formats compatible with that radio's stored profile, avoiding entries it cannot play. The full lookup uses each station's name/country/codec/bitrate and the shared delivery engine; Smooth's existing response fixture is preserved.

The radio's search menu searches stations assigned to that device. Public Radio-Browser discovery is a web workflow: search, select for an automatic stream check, then add to a radio with an optional favourite heart. My stations lists that radio's assignments, while Favourites lists its heart-marked subset. No speculative new manufacturer endpoint or JSON protocol is introduced. A fresh successful check can enable direct HTTP playback when the exact final URL is unchanged and remains HTTP; otherwise a legacy profile receives the local HTTP relay. Playlist responses are rejected by the relay.

## Podcast menus

The shared subscription library appears at `navXML.asp?gofile=Podcasts`. Shows use `ShowOnDemand`, including `ShowOnDemandID`, `ShowOnDemandName`, `ShowOnDemandURL` and its backup. Opening `navXML.asp?podcast=P<number>` returns `ShowEpisode` entries. Full episode lookup uses `Search.asp?sSearchtype=5&Search=P<number>X<episode-number>` and returns the show/title/description/format and an HTTP `ShowEpisodeURL` through `/episode/<id>`.

Show and episode IDs are short, monotonically allocated and stable across refreshes. Publisher GUIDs (or enclosure URLs when GUIDs are missing) identify existing episodes. Pagination uses the same one-based inclusive bounds and full ItemCount as station lists. The wire names and type-5 lookup come from [WiFi-RadioAPI’s on-demand documentation](https://github.com/kimbtech/WiFi-RadioAPI/blob/master/HamaAPI.md); fixtures and automated journeys verify our implementation. These are reference-derived responses rather than captured Pure podcast transactions. The owner confirmed podcast browsing and playback on the Pure ELAN IR5 on 4 October 2026.

On-demand XML preserves the captured field order: `ItemType` first, followed by identifiers, display text and links. Shows include an explicitly empty `BookmarkShow`. Episode lists and full lookups both include the complete on-demand fields, including empty Logo/bookmark/language/country values when unknown, and bound the radio description to 256 characters. Longer descriptions remain available in the web interface. Field order is tested separately from Go’s order-independent XML decoder.

Owner confirmed podcast browsing and playback on the Pure ELAN IR5 on 4 October 2026. Station and episode playback URLs now carry the radio ID for dashboard status, including when a reverse proxy masks the client address.
