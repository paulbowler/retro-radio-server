# Pure Play All

In **Internet Radio → Music → your music server**, a lowest-level folder containing only audio tracks has **Play All** above its tracks. Album tags and folder names do not affect the action. It plays all the folder's tracks, including later menu pages, in MinimServer order. Selecting a track starts there and continues through the remaining tracks. Higher-level/mixed folders keep single-track selections.

The radio receives one HTTP audio stream at `/stream/upnp-queue/<opaque-session>?radio=<id>`. Retro Radio opens each source in turn and keeps that connection open through track boundaries. Native MP3/AAC is reused; FLAC uses the existing MP3 converter. Leading MP3 ID3 file tags are stripped at each boundary. There are no M3U/PLS endpoints, new settings, album heuristics or diagnostic pages.

When the Pure requests `Icy-MetaData: 1`, the stream includes the current track's title, artist and available album in `StreamTitle` every 4,096 audio bytes. The server changes that text when the track changes. The display's song-information area should update; the directory station label and artwork are not pushed again. This still needs a physical-radio test. Clients which do not negotiate ICY receive plain audio and cannot get changing display information through this mechanism.

Delivery is paced by source duration/byte length when available, otherwise advertised bitrate (converted FLAC uses its fixed 128 kbps output). The fallback for missing timing is 128 kbps. This prevents sending entire NAS files instantly and advancing metadata far ahead of playback. Timing and transitions are approximate: this is not a gapless engine, and radio buffering may add delay.

A completed session returns 410 if the radio reconnects, so it cannot restart the same album automatically. Select Play All again for a new session. If interrupted before a track completes, reconnection restarts that current track. Seeking is not supported; an initial `Range: bytes=0-` is accepted. HEAD does not consume a session. Sessions are bounded to 128, expire after 24 hours idle and reset on restart; at most four continuous streams run together. Track folders are checked across pages, bounded to 1,000 tracks and 15 seconds, without scanning the NAS or recursively entering subfolders.

All tracks in a session must have the same output codec; mixed MP3/AAC output is rejected before playback. Native MP3 and converted FLAC can both produce MP3, but mixed-codec libraries are an edge case for this iteration. Unsupported tracks and changed source formats fail rather than adding new transcoding paths. Existing pinned NAS transports, opaque identifiers and radio playback attribution remain in use. Browser Music controls still preview individual tracks.

## Deploy and test

Update to the GitHub branch/commit containing this change and rebuild your existing service:

```sh
docker compose up -d --build
```

Keep the existing `.env`, database volume and DNS settings. This change needs no additional configuration or database migration. Previous experimental patches are superseded: use a clean checkout of the GitHub branch, retaining your deployment configuration, rather than layering those patches over it.

Reopen Music from the Pure's directory root after restarting the server, so links are fresh. Open a short folder with at least three recognisably different tracks:

1. Confirm Play All appears first and the final menu page contains each track once.
2. Select Play All; confirm track 1 → 2 → 3 without another selection and check that the song-information text changes.
3. Select track 2 from a freshly opened folder; confirm it plays track 2 and then track 3.
4. Confirm the final track does not restart the folder. Select Play All again to verify a fresh start.
5. Check ordinary radio stations, podcasts and browser music previews still play. Test a FLAC-only folder if that is your usual source.

If audio advances but the display does not, inspect whether the Pure sent `Icy-MetaData: 1` and received `icy-metaint: 4096`. A packet capture of the radio/server HTTP connection resolves that question; do not infer it from the static StationName label. Existing **Help → Download support report** can provide playback events; there is no requirement to restore an Activity page.

## Checks

`go test -race ./...`, `go vet ./...` and Linux amd64/arm64 builds run in GitHub Actions. Tests cover real menu-to-stream journeys, pagination, starts in the middle, mixed/missing album tags, continuous bytes, track-specific ICY blocks, attribution, MP3 tag stripping, cancellation/reconnection, HEAD/ranges, completed-session non-replay, unsupported folders and codecs. Generated native MP3 and FLAC queues are decoded by FFmpeg to verify both tracks; those tests skip when FFmpeg is absent. Physical Pure acceptance remains outstanding until the owner tests it.
