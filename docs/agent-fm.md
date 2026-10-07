# Agent FM

`Agent FM` appears on the Pure's **top-level Retro Radio menu**, beside Radio,
Podcasts and Music. It chooses music from the discovered UPnP audio servers;
you do not have to open an album or choose a playlist first.

The first track starts immediately after the library scan. While it plays,
the server sends candidate track metadata to OpenAI's Responses API to choose
the next track and write a short DJ link, then sends that text and delivery
instructions to the Speech API. The rendered voice is downloaded, normalized
and saved in a temporary MP3 file. At the end of the current track, the voice
plays and the chosen track follows on the same HTTP connection. The process
repeats until you stop or select something else. Completed music and speech
files are not retained. This first iteration plays links **between** tracks;
it does not talk over a song or crossfade.

## Enable it on your existing server

In the checkout containing `docker-compose.yml`, update main:

```sh
git pull --ff-only
```

Edit **`.env` beside `docker-compose.yml`** (create it from `.env.example` if
needed; preserve your existing settings):

```dotenv
RETRO_AGENT_FM=true
OPENAI_API_KEY=your-openai-api-key
```

Use an OpenAI Platform API key with billing and access to the text and speech
models. A ChatGPT login is not an API key. Keep `.env` private; it is ignored by
Git. Do not put the key in the Dockerfile, source code or browser. Limit local
access with `chmod 600 .env`.

Rebuild and recreate the application:

```sh
docker compose up -d --build
```

Compose passes the values into the application container. Changing `.env` then
just restarting a container does not update its environment; recreate it with
`docker compose up -d`. The discovery helper does not receive the API key.
For a non-Docker installation, supply the same environment variables to the
Retro Radio service and restart it. FFmpeg must be installed; the Docker image
already includes it. Agent FM's audio normalization is enabled independently
of the ordinary FLAC fallback setting.

## Radio test

1. Reopen the Pure's top-level Internet Radio / Retro Radio menu. Select
   `Agent FM` beside Music; it should not appear inside an album.
2. Allow the initial library scan (up to eight seconds). A music track starts.
3. At its end, listen for a brief AI-generated spoken link, followed by the
   announced track. The radio's ICY text changes from the music title to
   `Agent FM - Up next: …` and then to the new music title, provided the radio
   requests ICY metadata as it does for Play All.
4. Let several transitions play, then select another station. Preparation and
   temporary-file cleanup should stop with the old connection.
5. Try an invalid API key: Agent FM should still play music, skipping spoken
   links. `docker compose logs --tail=100 retro-radio` reports HTTP failures
   without printing credentials, API response bodies or track scripts.

If the option is missing, check `RETRO_AGENT_FM=true` and that `OPENAI_API_KEY`
is nonempty in `.env`, recreate the container and reopen the radio menu.
Startup logs say when Agent FM cannot be enabled. At least two compatible
UPnP tracks must be available to start the station.

## Voice and model settings

These optional `.env` values have defaults:

```dotenv
RETRO_AGENT_TEXT_MODEL=gpt-4o-mini
RETRO_AGENT_SPEECH_MODEL=gpt-4o-mini-tts
RETRO_AGENT_VOICE=ballad
RETRO_AGENT_DELIVERY="Speak as a warm British music presenter, conversational and relaxed, with natural inflection and varied rhythm."
```

Choose the voice in the web app's Settings (the cog button), under **Agent FM
voice**, and save. Ballad is the default. A saved voice overrides
`RETRO_AGENT_VOICE` and persists across restarts. Changes apply when the next
spoken link is prepared; a link already prepared keeps its original voice.
No container restart is needed for changes made in the web app.

A blank delivery setting uses built-in British DJ instructions. This is online
AI speech, not the operating system's speech voice. Both API requests incur
usage charges, generally one text call and one speech call per track transition
while Agent FM is playing. No API calls occur for menu browsing or HEAD probes.
The short link is an AI-generated voice; it is not a recording of a human DJ.

## First-iteration limits and fallback

- Each new session samples up to 1,000 playable tracks, across up to 200 UPnP
  browse pages, within eight seconds. It traverses discovered servers and
  folders, deduplicates object playback tokens, and randomizes traversal and
  candidates. Large libraries can yield a partial pool; this is not a complete
  persistent library index. Selecting Agent FM again builds a new pool.
- Each transition offers up to 100 candidates from that pool. The current
  track is excluded, and the previous 20 tracks are avoided when enough tracks
  remain. With a small library, older tracks can repeat. The agent receives
  title, performer, album and available composer/genre/date/duration tags, plus
  up to five recently played tracks; NAS URLs and audio files are never sent.
- Speech preparation has a 40-second total deadline. If it is not ready at the
  boundary, it is cancelled and music continues immediately. A valid track
  choice is kept if speech alone fails; invalid or late choices use a random
  available candidate. There are no retries or queued late announcements.
- Music and speech use a consistent 128 kbps, 44.1 kHz stereo MP3 stream. FFmpeg
  receives pinned NAS bytes or downloaded speech bytes, not untrusted URLs.
  Temporary speech files are private and deleted after use or cancellation.
- Selecting another station cancels work when the radio closes the old stream.
  Reconnecting resumes at the current chosen track from its beginning. An
  unavailable music source can end the connection; reselect Agent FM to build
  a fresh pool. Very long sessions may need restarting as NAS tokens expire.
- No live news, weather, events, lyrics or invented music history are requested
  in this iteration. Links prefer supplied metadata and may add well-established
  musical background from model knowledge when confident; there is no live fact
  lookup, so this is not independently verified research.

The API integration uses [Structured Outputs](https://developers.openai.com/api/docs/guides/structured-outputs)
and [Text to speech](https://developers.openai.com/api/docs/guides/text-to-speech).

## Validation

Tests cover the two API requests and voice instructions, bounded responses,
invalid track choices, API failures and cancellation; the Pure top-level menu
and lookup; one stream containing music → speech → agent-selected music with
changing ICY text; fallback music; late-job cancellation and file cleanup.
An FFmpeg integration test generates sources at different sample rates and
checks that the joined normalized MP3 segments decode together. The CI workflow
installs FFmpeg and runs these alongside the existing Play All tests.

### Artwork during playback

Agent FM uses the existing Retro Radio image throughout the session, served as
a radio-compatible 128 × 128 JPEG at `/artwork/agent-fm.jpg`. Track text still
changes through ICY metadata within one continuous audio connection.

The Elan IR5 did not refresh artwork from changing ICY `StreamUrl` values.
That experiment has been removed. Its local FSAPI controls can reselect a
station and trigger a new directory lookup, but a live test took about 2.5
seconds to report playback ready again; this was not a measurement of the
audible gap. We retain continuous playback rather than reconnecting at each
track. No radio IP address or control PIN is needed in the server configuration.

After upgrading, select another station and then select `Agent FM` again so
the Pure fetches the new fixed image.

## Genre stations

In Music → your NAS server → Genre → Jazz (or another genre), the Pure now
shows `[Jazz FM]` above the existing folders or tracks. The brackets mark this
as a playback action. The top-level `Agent FM` name has no brackets and still
samples across the music servers. `[Play All]` is unchanged.

A genre station traverses only that selected NAS genre view, including its
albums and item lists. The station and spoken-link metadata use `Jazz FM`,
`Classical FM`, etc. All stations share the saved voice and fixed Retro Radio
image, and retain continuous audio. No extra API key or configuration is needed.

The server recognises standard UPnP music-genre containers and MinimServer's
individual folders under its `Genre`/`Genres` index. Other folder types do not
receive an FM option. A genre needs at least two compatible tracks; it never
falls back to other genres when that requirement cannot be met. The existing
eight-second / 200-page / 1,000-track sampling limits also apply within a genre.
A track tagged with several genres can belong to more than one genre station.

The DJ prompt asks for the recognisable core song title, omitting bracketed
technical and release annotations (encoding, catalogue numbers, remaster
dates, etc.) while retaining parentheses that belong to the actual title.
For example, `So What [FLAC 24bit 96kHz] (2009 Remaster)` becomes `So What`
in speech, while `Don’t You (Forget About Me)` keeps its full title. The radio's
track text continues to use the NAS metadata. This spoken-title cleanup is
performed by the language model and should be checked with your own examples.

After rebuilding, reopen the Pure's menu, verify the unbracketed top-level
name, then open a genre and select its bracketed FM station. Listen through
several transitions to confirm the selected music stays within that genre.

## Listening in the web app

Open Music to find the Agent FM card, or browse into a NAS genre to find its
`[Jazz FM]`/`[Classical FM]` card. Press Listen to start a new browser session.
Both use the same DJ, voice setting, genre restrictions and Retro Radio image
as the Pure. The browser stream does not change what the Pure is playing.

Pausing, starting another player, or navigating away closes the Agent stream
and cancels preparation. Press Listen again to start a fresh session. The
Music page's automatic refresh preserves its Agent player. Opening the menu
or probing the audio endpoint with HEAD does not start a session or API calls.
Playback uses the web app's existing management authentication.

Browser audio uses the regular player and does not display the stream's ICY
track text; the card displays the station name. The Pure still receives the
changing track text as before.

Opening browser range probes such as Safari's `bytes=0-1` receive the complete
live stream with HTTP 200 and `Accept-Ranges: none`. The stream has no finite
length; seeking and multiple ranges remain unsupported.

## DJ voice volume and voice styles

In the web app's Settings, the voice picker groups the existing voices as male,
female or neutral by their perceived sound; these are app labels rather than
official OpenAI gender categories. Ballad remains available in the male group.
The [official voice options](https://developers.openai.com/api/docs/guides/text-to-speech#voice-options)
are unchanged.

DJ voice volume ranges from 25% to 400%. Existing installations keep 100%. Try
200% if spoken links are too quiet. This scales only the cached spoken announcement
locally before playback; music is unchanged. A limiter controls peaks when
volume is changed. Both settings persist across restarts, in browser, Pure and genre sessions.
Voice selection applies to the next link prepared. Voice volume is read when
the next announcement starts, including announcements already cached. There is no album-specific loudness matching.

## Track transitions

Agent FM retains one HTTP connection throughout music and speech; it does not
reselect a station at each boundary. While the current song plays, the server
prepares the DJ link, then opens and primes the chosen next NAS track. The next
track's bounded decoder pipe provides backpressure rather than caching an
entire album. Track metadata and playback history advance only at handover.
Stopping cancels preparation and closes the unused future audio.

This removes NAS lookup and decoder startup from the normal track boundary.
If preparation is late or prefetch fails, playback uses the existing fallback
and may still need to open a track. Silence contained in a recording remains
part of that recording. Speech-volume adjustments process the small cached
announcement locally at handover; they do not regenerate its voice online.

## Curated selection and track introductions

Agent FM and genre FM choose from shuffled candidates, rather than playing
an album in order. The DJ considers musical continuity, contrast, duration and
recent listening history, while avoiding consecutive performers/albums where
possible. Random selection remains the fallback when the online choice fails.
The opening track is random. Selection uses tags and model knowledge, not audio
analysis or measured tempo/energy.

Links introduce the recording's performer and core title, with a short specific
detail where known, rather than generic groove/vibes filler. DIDL artist roles
are preserved: performer/artist first, then unqualified track artist, then album artist;
composer/songwriter credits from artist or author tags are separate. This also corrects radio display names
when the NAS supplies both roles. Multiple performers at the preferred level
are retained. An ambiguous creator tag remains a fallback for legacy catalogues.

Incorrect, unqualified artist tags cannot reliably identify a recording: the DJ
is asked to avoid uncertain credits and never replace a cover performer with
its famous original artist. Correct the NAS tags if the wrong name persists.
Exact reissue dates are not treated as original release dates. Uncertain song
history is omitted. These editorial instructions guide AI output; their factual
accuracy and musical taste still need listening checks on the actual library.

MinimServer documents these role-bearing tags in its
[displayRole settings](https://minimserver.com/ug-other.html); composer credits
can appear as `upnp:artist role="Composer"` or `upnp:author role="Composer"`.
