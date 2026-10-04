# Device compatibility and hardware acceptance

| Device | Directory | Audio | Status |
| --- | --- | --- | --- |
| Pure ELAN IR5 | Initial login path observed; detailed navigation checks remain open | Smooth playback confirmed by owner, 3 October 2026 | **Core playback hardware-confirmed** |
| Hama DIR3100MS | Public reference documentation used | Not tested here | Reference only |
| Other Frontier XML radios | Manufacturer path adapter available | Not tested | Experimental |
| Frontier JSON, vTuner, Reciva | No adapter | No claim | Future work |

Do not interpret a matching User-Agent, manufacturer path or incoming request as proof of an exact model. Registration does not identify an exact model; users name their radios in the web interface. No actual Pure, DHCP/DNS service or audio output is available in the development workspace.

## Acceptance checklist — owner with physical ELAN IR5

1. Run the server on a LAN host reachable on TCP port 80. Confirm `http://<server-IP>/healthz` returns `ok`.
2. Rewrite only `pure.wifiradiofrontier.com` to that LAN IPv4 address. Make sure the radio uses that DNS resolver; restart the radio. Do not rewrite broadcaster, NTP or firmware hosts.
3. Open Internet Radio. The radio should appear under Radios. A challenge alone is not sufficient to register a device.
4. Confirm **All stations**, **By country**, **By genre**, **Favourites** and **Search stations** appear. Open All stations; confirm **Smooth Radio** appears. Test the Back button.
5. Select Smooth Radio. Confirm the station selection and playback in Activity; detailed connection events are included in the support report. Confirm the radio makes an HTTP `/stream/<opaque-id>` request to this server and no direct HTTPS upstream request.
6. **Listen for audio for at least five minutes.** Correct station XML and a connected socket do not prove sound. Note stalls, ID3/ICY display and reconnect behaviour.
7. Stop playback. Confirm Stream disconnected; select the station again and verify reconnect. Power cycle the server/radio and confirm stable stream IDs and persisted device names/favourites.
8. Assign “Kitchen” in the web UI and add Smooth to that device's favourites. Confirm radio favourites browsing/playback. Search for “Smooth”. Record actual query parameter spelling and firmware version.
9. If a second radio is available, check device/favourite isolation. Directory ID hashes must be distinct even if IP changes.
10. Download diagnostics. Record real model, firmware, date, port/DNS arrangement and each result below. For failures, supply a **redacted** HTTP transaction trace with identifying `mac`/serial/token values removed.

## Result sheet

```text
Date:
Model / firmware:
Server version:
DNS resolver:
Challenge accepted: PASS / FAIL
Root + Back navigation: PASS / FAIL
Catalogue and station lookup: PASS / FAIL
Five-minute audible Smooth playback: PASS / FAIL
HTTP-only downstream: PASS / FAIL
Stop/reconnect: PASS / FAIL
Web favourite then radio browse/play: PASS / FAIL
Search submission: PASS / FAIL
Notes / redacted trace:
```

The owner confirmed audible Smooth playback on a physical Pure ELAN IR5 on 3 October 2026, passing the core milestone playback gate. This checklist remains useful for broader regression coverage; listening duration, firmware and individual navigation/favourite checks have not been reported. Radio-Browser, custom stations and adaptive audio conversion are implemented; podcasts remain future work.

## Development verification

Automated tests cover synthetic reference-based transactions, capability selection, database restart and favourite isolation, public/private address policy, upstream scheme/port checks, HTTP/1.0 Range playback, HTTPS/chunked ICY bytes, immediate delivery and disconnect cancellation. TLS fixtures use a **test-only** transport to reach loopback; production has no bypass flag.

## Milestone 2 trial

Search Radio-Browser in the web UI, choose the actual radio and save a compatible MP3 station to favourites. Check its stream, then browse Favourites on the Pure and listen. Restart the service while retaining its data volume and confirm the favourite remains. Repeat with a custom station. Test Back, full station names, accents and search/favourite navigation on the hardware. This new hardware workflow is not yet owner-confirmed; only the earlier Smooth playback is.

The software workflow is tested end to end using a synthetic radio, including migration and restart persistence. Live imported Retro Rádió audio was delivered over the local HTTP relay. These observations do not substitute for a physical-radio listening test.
