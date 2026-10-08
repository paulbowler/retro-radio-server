# Ultimate 80s comparison (8 October 2026)

Compared files on the mounted Music share and the live radio application's
published album, artwork and track endpoints. Files on the NAS were read only.

| Property | Ultimate 80s | NOW That's What I Call The 80s | Buena Vista Social Club |
| --- | --- | --- | --- |
| Music files | 72 FLAC | 100 MP3 | 14 FLAC |
| FLAC audio in sampled files | 44.1 kHz, 16-bit, stereo | Not applicable | 44.1 kHz, 16-bit, stereo |
| Representative embedded cover | 2850 × 2850 JPEG, 775,951 bytes | 600 × 600 JPEG, 49,523 bytes | 500 × 494 JPEG, 68,221 bytes |
| Cover stored inside files | Present in all 72 FLAC files | Present in all 100 MP3 files | Present in inspected FLAC files |
| Live app artwork response | HTTP 502 | HTTP 200 JPEG | HTTP 200 JPEG |
| Live audio without a Range header | HTTP 200 MP3 conversion | Native MP3 | HTTP 200 MP3 conversion |
| Initial browser byte range | `bytes=0-1`: HTTP 416 | `bytes=0-8191`: HTTP 206 | `bytes=0-1`: HTTP 416 |

Representative live tracks: Ultimate's **I Wanna Dance with Somebody (Who Loves
Me)**, NOW's **I Want to Break Free (Single Version)**, and Buena Vista's **Chan
Chan**. Reads of audio were short samples, not complete listening/decoding tests.
The owner reports playback failure in Safari on iPhone/iPad.

## Confirmed cover failure

Ultimate has 8,122,500 cover pixels. `artwork.RadioJPEG` allowed only 4,194,304
pixels, so the valid embedded cover was refused even though its compressed JPEG
fits the UPnP endpoint's one-MiB byte limit. Both comparison albums have much
smaller images. This is an application decode limit, not missing cover tags or
stale SQLite data.

The actual embedded Ultimate JPEG was extracted to a temporary file, then checked
against both paths: the old public-artwork path rejected it; the new private
album-artwork path produced a valid 128 × 128 JPEG (8,016 bytes).

Private UPnP covers now allow at most 4096 pixels on each axis and 16 megapixels.
The existing byte/download/scope restrictions remain, and at most two private
covers decode concurrently across providers. Public station artwork keeps its
four-megapixel limit. The NAS files and embedded covers are unchanged.

## Confirmed browser-request defect

Ultimate and Buena Vista both successfully supplied converted MP3 bytes on a
normal GET. Both failed an opening `Range: bytes=0-1` with HTTP 416. Ultimate also
worked with `bytes=0-`. The converted-track endpoint previously accepted only
that open-ended spelling, while Play All already accepted bounded opening probes.

Converted single-track playback now shares Play All's opening-range handling:
empty ranges and valid ranges starting at byte zero return the full HTTP 200
stream. Nonzero, suffix, malformed and multiple ranges are still rejected;
converted seeking is not offered. Native MP3 range delivery is unchanged.

This is a shared FLAC conversion endpoint defect consistent with the reported
Safari device. It does not prove that Ultimate alone has a broken FLAC encoding,
and there is no reason to convert or retag its source files based on these tests.
The exact failing Safari request was not captured from the owner's device, so
successful playback after deployment remains an acceptance check.

## Deployment and validation

On the deployed checkout, pull `main` and rebuild the application:

```sh
git pull origin main
docker compose up -d --build retro-radio
```

Reload Safari's Music page, reopen Ultimate 80s, and try a track. Do not delete the
database again: these two defects are in request/image handling, and database
replacement cannot change them. The live deployment remains unchanged until the
updated image is built and started.

Regression coverage checks Ultimate-sized artwork, unchanged public limits,
invalid image dimensions, successful zero-start range responses, rejected real
seek/malformed ranges and converter cleanup. Artwork, UPnP and web tests pass with
race detection. Real FFmpeg integration tests are skipped on this Mac because
FFmpeg is not installed; the live server's existing FFmpeg conversion returned
MP3 data for both representative FLAC tracks.
