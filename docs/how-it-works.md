# How subflux works

This page explains how subflux finds, scores, times and saves subtitles, and what it does when something fails. It is for readers who want to know why subflux picked a subtitle or paused a site.

## When subflux searches

subflux reads your library from Sonarr and Radarr and keeps no library of its own. It checks their import history every 30 seconds by default (`poll_interval`), so a new download gets subtitles soon after it is imported. A full library scan runs once a day by default (`search.scan_interval`), 30 seconds after the container starts and then 24 hours after the previous scan ends. It fills gaps and looks for better matches.

A full scan that is cut short by a restart resumes where it stopped. Items scanned within the last `scan_interval` are skipped. Before each full scan, subflux compares its database with the files on disk, so it corrects itself after you add or delete subtitle files by hand.

## Search and scoring

subflux searches eight subtitle sites through one interface: OpenSubtitles, Gestdown, SubSource, SubDL, BetaSeries, AnimeTosho, YIFY Subtitles and HDBits. It also reads the subtitle tracks already inside each video file with ffprobe. That is local inspection, not a site.

Each result goes through two checks:

1. An identity check. The IMDb, TVDB or TMDB ID, the season and episode, and the title must match.
2. A release-quality score from 0 to 100, which ranks the results and decides upgrades.

The best result is downloaded, timed, cleaned up and saved next to the video. An upgrade replaces an existing subtitle only when a strictly better release appears, and only within `search.upgrade_window_days` of the download, 7 days by default.

## Languages

Language rules are keyed on the audio track. You map each detected audio language to the subtitle languages you want. Japanese audio can get different subtitles than English audio, for example. Each target can ask for the `standard`, `forced` or `hi` variant, and can override the sites it uses or its minimum score. A fallback list covers files whose audio matches no rule.

## Anime numbering

Searches run with the aired, scene and absolute episode numbers and merge the results. Scene numbers come from TheXEM and absolute numbers from TVDB. Long-running shows with unusual episode orders still match.

## Embedded subtitles

Coverage counts the text and picture-based subtitle tracks already inside the video: SRT, ASS, PGS, VobSub and DVB. The `embedded_subtitles` section decides which codecs count as a usable subtitle. PGS and VobSub tracks are ignored by default, so a picture-based track does not stop the search for a text one. Ignored tracks still show in coverage.

## Backing off

Each site gets its own retry backoff for items with no results, and a timeout after repeated failures, one hour by default (`search.provider_timeout`). A season stops searching early when it is clear the site has nothing for it. A failing or empty site is not asked again and again.

## Manual downloads and locks

A download you pick by hand is saved as a numbered file next to the automatic one, for example `movie.fr.1.srt`. Picking anything other than the top result locks that item and variant from automation. Picking the top result keeps automation on, because it is what automation would have chosen. A lock clears when you delete the manual files, when Sonarr or Radarr replaces or deletes the video, or when you release it with `subflux unlock` or `POST /api/search/clear-lock`.

## Timing

Downloaded subtitles rarely match your exact file, so subflux re-times each download before it reaches the disk. The engine is a Go port of [alass](https://github.com/kaegi/alass), with these additions:

- Alignment that handles splits and frame-rate differences.
- Audio timing, which compares a voice-activity detector's output with the subtitle's dialogue. The detector is ported from WebRTC and re-tuned for film audio.
- Cross-language matching, so a French subtitle can be timed against the English track inside the file.

The methods run at the same time and vote. The winner is applied only above a confidence threshold, so a timing change subflux is not sure of is not made.

Automatic downloads are timed against a subtitle track inside the video when the file has one. Timing against the audio when there is no such track is off by default (`post_processing.audio_sync_fallback`). Audio timing and manual offsets are always available from the timing dialog on the web page.

Downloads matched by file hash or from the same release skip timing, because their timing is already right. Forced subtitles skip it too, because they have too few lines to align.

## Clean-up

Every saved file is converted to UTF-8, has HTML tags and extra whitespace removed and gets standard line endings. Hearing-impaired annotations are removed only when `post_processing.strip_hi` is on. The `post_processing` section in [Configuration](configuration.md) turns each step on or off.

## The web page

The web page is served by the same program and updates live as subflux works. It is written in TypeScript with no framework, loads as native ES modules and receives updates over server-sent events.

- The coverage table lists every show and movie against your language rules, with have and total counts per language, embedded-track counts, a missing-only filter and a text search.
- The timing editor streams the video at 360p, using the bundled FFmpeg, with the subtitle as a live caption track. Move the offset with a timecode control and the captions reload in place. You can also run any timing method and preview its result before anything is written.
- Manual search queries every site for an item, shows each result's score breakdown and lets you download a specific result.
- The settings dialog renders every setting with a tooltip. A save is checked before it is applied and takes effect without a restart.
- History lists every download and search, filtered by type, language and site.

Until a valid configuration is saved, the settings dialog opens on its own and the page offers nothing else.

## Rejected credentials

When a site rejects its credentials, such as a wrong password, passkey or API key, subflux stops calling it instead of retrying until the site locks the account. The first rejection pauses the site for 5 minutes and the second for 30 minutes. Each pause ends with one test request, and the third rejection disables the site.

A disabled site raises an alert on the web page that stays until it is cleared, logs one ERROR line and sets `subflux_provider_disabled{provider}` to 1. The disable survives a restart, and nothing turns the site back on by itself. Three things do:

- Save the site with different credentials.
- Press the site's **Test** button in the settings dialog. A pass with the recorded credentials turns the site back on. A pass with edited credentials asks you to save them.
- Reset site state with `subflux timeouts-reset`.

Switching the site off clears its alert, and switching it back on with the same credentials disables it again. A rate-limit answer is not a rejection. It pauses only the operation that hit it, a search or a download, for as long as the site's `Retry-After` header says, or 10 minutes.

AnimeTosho's optional AniDB client key is judged on its own. If AniDB rejects the key, AnimeTosho stops looking up episodes by AniDB ID and keeps searching by title. An alert names the key, and `subflux_provider_setting_rejected{provider,setting}` reads 1. This lasts until one of these happens:

- You change or clear the key.
- A Test of the saved key passes.
- You reset site state with `subflux timeouts-reset`.
- AniDB accepts the key on a later lookup.

A restart forgets the refusal. The alert and the metric return after the first episode lookup AniDB refuses again. Saving any settings, a passing Test and a reset each make AnimeTosho ask AniDB again on its next episode lookup.

A scan that is already running when you save keeps the settings it started with. It does stop calling any site whose settings you changed. The next scan uses the new settings.

## Media folders that cannot be written

subflux saves subtitles next to the media, so a read-only media folder prevents any subtitle from being saved. It checks each folder by writing and deleting a small hidden file:

- in each media root before a full scan,
- in the folder a single-item scan, an import or a manual download writes into, before it starts,
- in a folder where a save just failed,
- every 5 minutes in a folder found unwritable.

When subtitles cannot be written in a folder, subflux stops searching for and downloading subtitles there. It shows an alert naming the folder and the error, and `subflux_media_root_unwritable{root}` reads 1 for that folder's media root. It resumes once a write test there succeeds. subflux never changes file or folder permissions. New subtitle files get the permissions your share's umask and ACLs give them.

While such a folder stays unwritable, later imports from the same Sonarr or Radarr instance also wait, whatever folder they land in. That includes the other imports reported in the same check. None is lost. They are processed in order once the folder recovers, and the next full scan covers healthy folders meanwhile. A waiting import is also released once it no longer needs a subtitle there. That happens when you tag its series with one of your `search.exclude_arr_tags`, or when you delete its video. subflux checks it again at most 5 minutes plus one `poll_interval` after its last check.

If a configured media root is missing or cannot be written, full library scans do not start until it is fixed or removed from `media_roots`.

## Missing and unmounted media

A video counts as deleted only when it is missing from a media root that is present, readable and not empty. An unmounted share usually looks like an empty root. If the root is missing or empty, or a file check fails or takes over 10 seconds, subflux keeps the subtitle records and manual locks. It also holds new imports there, shows an alert naming the path and checks again every 5 minutes. `subflux_media_root_unavailable{root}` reads 1 meanwhile.

List each mounted share as its own `media_roots` entry, so an unmounted one shows up as an empty root.

## Resource use

subflux is one static Go program with no Python and no runtime dependencies, on a distroless base image. Sonarr and Radarr answers are fetched in full and then processed, worker pools have fixed sizes and media files are read as a stream. The largest answer measured, 4,360 movies, decodes to 24 MB.

The bundled FFmpeg is about 5 MB, plus about 2 MB for ffprobe. It holds decoders for the common video and audio codecs and for SRT, ASS, MOV text, WebVTT, PGS, DVD and DVB subtitles, one x264 encoder for the 360p preview, and no network support. It detects tracks, extracts subtitles and audio and streams the preview.

All state is one file, `/config/subflux.bolt`, kept with bbolt, a database written in Go. A write is safe on disk once it returns. Scheduled backups are available in the `backup` section.

## Limits

- Sites behind Cloudflare protection, such as subf2m, AvistaZ and CinemaZ, are not supported.
- Long-running anime whose aired and absolute numbers collide can, rarely, get a wrong match. Results matched by a stable ID skip the title check, so an aired SxxEyy that equals another episode's absolute number can slip through.
- Shows with no TheXEM mapping are searched with aired numbers only.
