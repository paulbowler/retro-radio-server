# Agent FM

`Agent FM` appears on the Pure's **top-level Retro Radio menu**, beside Radio,
Podcasts and Music. It chooses music from the discovered UPnP audio servers;
you do not have to open an album or choose a playlist first.

After the library scan, each new Agent FM or genre FM session opens with
“You're listening to Retro Radio. Welcome to [station name]. Good music,
thoughtfully chosen. Let's begin.” The bracketed placeholder is replaced with
Agent FM, Jazz FM or the chosen genre station name; brackets are not spoken.
The welcome is generated once per station and stored as an MP3 in `agent-intros/`
beside the database (`/data/agent-intros/` in Docker's existing persistent data
volume). Later sessions and server restarts reuse it without another speech API
request. It uses the voice selected when first generated; subsequent voice
changes affect new DJ links, while the stored station welcome stays unchanged.
The saved DJ volume applies each time the welcome plays. To deliberately recreate
a welcome with a different voice, remove its stored MP3 and restart the server.

For the Pure, the welcome is a short, finite stream. At its end the radio reconnects
to the same queue URL, which then serves the continuous music programme. A brief
pause between the welcome and music is acceptable; tracks and DJ links never
require another stream switch. The web player receives the welcome followed by
music in one connection. Menu browsing and HEAD probes do not generate welcomes.
If welcome preparation fails, music starts instead and the server logs the error.

While the first track plays,
the server sends candidate track metadata and recent programme context to
OpenAI's Responses API to choose the next track and write a DJ link, then
sends the script to the Speech API. The downloaded voice is compressed and loudness-levelled, then decoded into a
private temporary PCM cache. Music and speech pass through one continuous MP3
encoder: the DJ overlaps the last three seconds of the song where possible,
with the music ducked, and the chosen next track follows without a new stream.
Preparation repeats until you stop or select something else; temporary audio
files are deleted after use.

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
already includes it. Agent FM's programme decoding runs independently of
the ordinary FLAC fallback setting.

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
RETRO_AGENT_TEXT_MODEL=gpt-6.1-sol
RETRO_AGENT_SPEECH_MODEL=gpt-4o-mini-tts
RETRO_AGENT_VOICE=ballad
RETRO_AGENT_DELIVERY="Speak as a warm British music presenter, conversational and relaxed, with natural inflection and varied rhythm."
```

GPT-6.1 Sol is the default text model for the more demanding music-editorial
prompt. Existing `.env` files that explicitly set `gpt-4o-mini` retain that
choice: change `RETRO_AGENT_TEXT_MODEL=gpt-6.1-sol` and recreate the container
to upgrade. For the cheaper alternative, use `RETRO_AGENT_TEXT_MODEL=gpt-6-luna`
(the currently documented Luna API name is GPT-6 Luna, not GPT-6.1 Luna).
Both models use low reasoning effort and a bounded 2,048-token output budget,
including reasoning, within the same 40-second preparation deadline. Speech
and optional local research keep their existing separate models. Text, speech
and web search charges are separate; see the current
[model prices](https://developers.openai.com/api/docs/pricing).

### Presenter personality and memory

The DJ is a warm, curious British music enthusiast with understated humour
and a considered musical point of view. Links vary in length and editorial
angle: interpretation, arrangements, career context, musical influences or
connections between records, rather than stock energy/mood transitions.
The usual target is 35–80 words, with shorter identifications when little is
known and occasional longer stories. Local updates allow up to 120 words.

Each station queue remembers its last twelve links inserted successfully into
the audio programme, oldest first, alongside five recent tracks. The next
request receives this context to avoid repeated phrasing, stories and facts,
and occasionally develop a previous musical thread. Failed, late, expired or
unavailable speech is not remembered. Recording happens after audio insertion
so the next preparation knows about the preceding link even when the encoder
is slightly ahead of playback. This cannot confirm what a disconnected radio
actually heard. Memory stays with a queue across reconnections; selecting a
new station or restarting the server starts fresh. Stations do not share it.
It is bounded and held in memory, with no transcript database or extra API
calls. Previous scripts are context, not factual evidence. Metadata, not
music audio or NAS URLs, is sent to the text model.

The model may use confident, established music knowledge, but this change does
not add recording research or guarantee factual accuracy. It must distinguish
performer from composer, avoid invented listening observations and personal
biography, and keep unfamiliar recordings' introductions grounded in tags.

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
- Speech preparation has a 40-second total deadline. If it is not ready before
  the buffered music tail, it is cancelled and music continues immediately. A valid track
  choice is kept if speech alone fails; invalid or late choices use a random
  available candidate. There are no retries or queued late announcements.
- Music and speech use a consistent 128 kbps, 44.1 kHz stereo MP3 stream. FFmpeg
  receives pinned NAS bytes or downloaded speech bytes, not untrusted URLs.
  Temporary speech files are private and deleted after use or cancellation.
- Selecting another station cancels work when the radio closes the old stream.
  Reconnecting resumes at the current chosen track from its beginning. An
  unavailable music source can end the connection; reselect Agent FM to build
  a fresh pool. Very long sessions may need restarting as NAS tokens expire.
- Music-only links prefer supplied metadata and may add well-established musical
  background from model knowledge when confident. Local news/weather/events use
  the optional live research described below. Lyrics are never requested.

The API integration uses [Structured Outputs](https://developers.openai.com/api/docs/guides/structured-outputs)
and [Text to speech](https://developers.openai.com/api/docs/guides/text-to-speech).

## Validation

Tests cover the two API requests and voice instructions, bounded responses,
invalid track choices, API failures and cancellation; the Pure top-level menu
and lookup; one stream containing music → speech → agent-selected music with
changing ICY text; fallback music; late-job cancellation and file cleanup.
An FFmpeg integration test generates sources at different sample rates and
checks that a single programme encoder handles them. PCM tests measure saved
25% versus 400% gain, exact overlap length, ducking and bounded silence trimming.
FFmpeg tests measure gain after MP3 encoding and consume the full NAS → music/
speech mixer → MP3 → ICY path, including cancellation and changing track text.
The CI workflow installs FFmpeg and runs these alongside the Play All tests.

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
in the PCM mixer; music is ducked only while speech overlaps it. A peak clamp
prevents overload. Both settings persist across restarts, in browser, Pure and genre sessions.
Voice selection applies to the next link prepared. Voice volume is read when
the next announcement starts, including announcements already cached. There is no album-specific loudness matching.

## Track transitions and voice gain

Agent FM uses one HTTP connection and **one MP3 encoder for the entire session**.
Music and cached speech are decoded to 44.1 kHz stereo float PCM before mixing.
The final output remains 128 kbps MP3; mixed source codecs need no new NAS setup.
There is one pacing clock, with no per-song encoder reset or end-of-segment wait.

While a song plays, the server prepares the DJ link and primes the chosen next
NAS decoder. A bounded five-second music tail lets the mixer start the chat over
up to the final three seconds of actual music, with music at 25% underneath.
If chat lasts longer, it continues after that music ends, then the next song
starts directly. Short tracks have a shorter overlap. If the link is late or
fails, music continues without the chat. Preparation is checked before the
buffered tail is sent; no online work is awaited at that boundary.

Up to two seconds of near-digital silence below -60 dBFS are removed from track
edges and cached speech edges. Quiet music above that threshold is preserved;
longer intentional silence is not removed in full. Source stalls, missing next
tracks or incomplete online preparation can still affect playback. Decoder pipes
and the five-second tail bound memory; no whole album is cached.

Downloaded speech first passes through a gentle compressor (3:1, -30 dBFS
threshold, 5 ms attack, 100 ms release) and FFmpeg's EBU R128 loudness levelling
with a -14 LUFS target, -2 dBTP ceiling and 7 LU loudness range. This happens
while preparing the private speech cache, never at handover. Quiet syllables
are brought up rather than relying on a few loud peaks. Different voices and
API output levels start from a consistent baseline; music is not levelled.

The saved DJ volume then scales those levelled speech PCM samples, immediately
before mixing. **100% now means the levelled voice, not the original API
recording.** After upgrading, start at 100% and adjust to taste; an existing
400% setting is preserved and can be much louder than before. At non-clipping
levels 400% is four times the amplitude of 100%; 25% is
one quarter. Music is unchanged apart from the deliberate ducking during speech.
The final mix is peak-clamped to prevent overload. There is no automatic loudness
normalization after the gain that could undo it. Changes can affect speech already
cached, without regenerating the voice online. Save the setting in the app.
Server logs report the applied voice gain and overlap at each spoken link.

ICY titles and playback observations follow positions in the encoded programme,
rather than the ahead-of-playback decoder. Stopping cancels the encoder, online
preparation and future NAS decoder and removes private speech files. The fixed
Retro Radio artwork remains unchanged.

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

## Previous track and occasional local updates

Each spoken link now starts with a brief back-reference to the performer and
core title of the current song, then introduces the next track. Because the link
starts over the song's tail, the DJ uses wording such as “That's …” rather than
claiming that the song has already ended.

After rebuilding, open **Settings**, enable **Occasional local news, weather and
events**, enter **Winchester, UK** under **Your locality**, and Save. This saved
override is shared by Pure, browser and genre sessions and survives restarts.
It always wins over automatic lookup. Local updates and automatic lookup both
start off; enabling local updates sends the locality and optional source-page
addresses to OpenAI web search using the existing API key.

If the locality is blank, the separate **Allow approximate public-IP location
lookup** setting permits an HTTPS request to [ipwho.is](https://ipwhois.io/documentation)
from the radio server. The service sees its public IP and returns an approximate
town. The town is used for web research, without sending the IP itself in the
OpenAI input. This estimates the server's network, not a phone browser's location,
and can be wrong with ISP gateways, VPNs or remote hosting. Leave this control off
when using the Winchester override. The settings show the saved/estimated place.

Research uses [OpenAI web search](https://developers.openai.com/api/docs/guides/tools-web-search)
with `gpt-4.1-mini` by default (`RETRO_AGENT_LOCAL_MODEL` overrides it). This adds
search/model API charges; no second API key is needed. Only active link preparation
starts research. One background job runs at a time, with a 25-second deadline,
and refreshes at most every 30 minutes (15 minutes after failure). It can finish
a bounded refresh after listening stops; there is no recurring idle scheduler.
Playback never waits for local research.

The researcher is asked to focus roughly 15 km around the locality, preferring
council, venue/organiser, official weather and established local-news sources.
It looks for recent news, worthwhile events in the next seven days, and notable
weather changes/warnings today. Ordinary weather or a quiet news day can yield
no update. Public social posts may serve as leads if posted by an organiser or
corroborated. Optional HTTPS source addresses in Settings steer the search;
these are public search leads, not authenticated RSS/social subscriptions.
Private groups, sign-in-only feeds and unindexed posts cannot be read.

Only source URLs returned by search/citations qualify. Date checks reject old
news, past/distant events and forecasts for another day. Cache validity is at
most six hours for news/events and one hour for weather; it is checked again at
actual handover. Turning updates off or changing locality cancels pending
research and discards old cached local links. The last researched items link to
their publishers in Settings; the DJ attributes the spoken update but does not
read URLs aloud. Geographic relevance and the source summary still rely on the
research model, rather than a measured geofence or independent fact-checker.

One item at most is offered per link, with at least 20 minutes between prepared
local links. The cooldown also restarts when that link reaches the outgoing
stream, so a long classical track followed by a short song cannot cause closely
spaced updates. Story URL/kind/date deduplication lasts 48 hours in server memory
and resets on restart. A failed or skipped spoken link conservatively consumes
its offer rather than repeatedly retrying the same story.

Links with a local item follow: previous song → brief attributed local update →
next track introduction (55–100 words). Other links remain music-only (30–65
words). The continuous encoder, three-second overlap and voice-volume control
are unchanged. Music continues if research is unavailable.

### Voice loudness investigation (October 2026)

A bounded capture from the running server, with its saved volume and logs both
showing 400%, measured a music excerpt at -8.87 LUFS and the spoken excerpt at
-13.84 LUFS. The speech true peak was already -0.72 dBTP. This demonstrated a
roughly 5 LU balance deficit with little peak headroom, despite the multiplier
being applied. Tone-only gain tests did not cover that speech dynamic range.
The speech-only processing above addresses this; it does not increase the
slider's range or change the continuous programme encoder.

Regression tests now also exercise speech-like quiet syllables, pauses and
loud peaks at different source levels, and measure the actual encoded output
at saved 25%, 100% and 400%. Music-only decoding remains unchanged. Real radio
listening after deployment is still required; a single captured link is not a
measurement of every voice or album.
