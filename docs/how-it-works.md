# How subflux works

This page explains how subflux finds, ranks, times and saves subtitles, and what it does when something fails. It is for readers who want to know why subflux picked a subtitle or paused a site.

## When subflux searches

subflux reads your library from Sonarr and Radarr and keeps no library of its own. It checks their import history every 30 seconds by default (`scan.change_interval`), so a new download gets subtitles soon after it is imported. A full library scan runs once a day by default (`scan.whole_interval`), 30 seconds after the container starts and then 24 hours after the previous scan ends. It fills gaps and looks for better matches.

A full scan that is cut short by a restart resumes where it stopped. Items scanned within the last `scan.whole_interval` are skipped. Before each full scan, subflux compares its database with the files on disk, so it corrects itself after you add or delete subtitle files by hand.

## Search and fit

subflux searches thirteen subtitle sites through one interface: OpenSubtitles, Gestdown, SubSource, SubDL, BetaSeries, AnimeTosho, YIFY Subtitles, HDBits, TsukiHime, Jimaku, Subs.ro, Assrt and SubX. It also reads the subtitle tracks already inside each video file with ffprobe. That is local inspection, not a site.

Each result first goes through an identity check. Nothing the site says may contradict the item: the IMDb, TVDB or TMDB ID it matched, the season and episode, the episode title and the year. A result the site found for the show or movie but cannot place on the episode is offered in manual search only.

subflux then compares the result's release name with the video's and grades how closely they match. There is no score. The grade is one of five fit classes, best first:

1. `exact`: the site matched the video's file hash.
2. `same_release`: the same release group, with no difference in source, streaming service or frame rate.
3. `same_source`: the same source, such as BluRay, WEB or HDTV, with no difference in frame rate.
4. `other`: anything else.
5. `conflict`: a different cut, such as an extended edition, or a cam or screener against a retail release. A conflicting result is never downloaded automatically.

`search.min_fit` sets the lowest class downloaded automatically, `other` by default, and `search.min_fit_by_target` sets it per language. Results are ranked by language first: the exact language before a close variety, a human-made subtitle before a machine translation, the closest variety, and a subtitle made for the variant before one that only serves it. Fit comes next. Equal results go by frame rate, streaming service and cut, then a single episode before a season pack, then the site's `priority` and name.

The best result is downloaded, timed, cleaned up and saved next to the video. subflux tries at most 3 results for one language and variant in a search. A result it already tried is not tried again for 30 days, or for the upgrade window when that is longer, unless its download failed.

An upgrade replaces a subtitle subflux downloaded automatically when a strictly better one appears:

- a higher fit class, within `search.upgrade_window_days` of the download, 7 days by default;
- a human-made subtitle over a machine translation, at any age;
- the exact language over a close variety;
- a subtitle with verified timing over one saved as "Timing unverified".

A subtitle you downloaded or uploaded by hand, a locked one and a file subflux did not write are never replaced.

## Languages

Language rules are keyed on the audio track. You map each detected audio language to the subtitle languages you want. Japanese audio can get different subtitles than English audio, for example. Each target can ask for the `standard`, `forced` or `hi` variant, and `search.min_fit_by_target` can give it its own minimum fit. A fallback list covers files whose audio matches no rule.

## Episode numbering

Searches use each episode's TVDB season and episode and its absolute number. The series data of Sonarr's metadata service also gives subflux the episode titles and the places of each episode in the show's other orders, such as the DVD order. A subtitle, or a member of a season pack, numbered in another order is matched by its episode title. A pack whose members carry numbers and no titles, on a show with more than one order, is checked against the video's timing: a member whose timing does not fit is refused, and with no subtitle track in the video to time against, the pack is refused.

AnimeTosho finds an anime episode by its AniDB episode, read from [animap](https://github.com/cplieger/animap)'s anime map. subflux downloads the map from GitHub on the first AnimeTosho search, and checks for a newer release when a search finds the copy it holds 6 hours old. A failed download keeps that copy, waits 30 minutes before the next try and never counts against AnimeTosho itself. When the map cannot be downloaded at all, or has not refreshed for 24 hours, an alert says so. With no map, AnimeTosho searches by title.

## Embedded subtitles

Coverage counts the text and picture-based subtitle tracks already inside the video: SRT, ASS, PGS, VobSub and DVB. `embedded_subtitles.ignore_codecs` decides which formats do not count as a usable subtitle. PGS and VobSub tracks are ignored by default, so a picture-based track does not stop the search for a text one. Ignored tracks still show in coverage.

## Backing off

When a site answers that it has nothing for an episode in a language, automatic searches do not ask it again for that episode and language for 7 days. Each further empty answer doubles the wait, up to 2,190 hours, about 3 months. Once 3 other episodes of a season are waiting on a site, the season's other episodes stop asking it too. A site that finds something ends the wait.

A site that fails 5 times within 10 minutes, by timing out, refusing the connection or answering with a server error, rests from automatic searches for an hour. Manual searches still ask it. A rate-limit answer pauses the search or the download that hit it for as long as the site's `Retry-After` header says, or 10 minutes, and at most 24 hours.

## Manual downloads and locks

A download you pick by hand, or a subtitle file you upload, is saved as a numbered file next to the automatic one, for example `movie.fr.1.srt`, and locks that item's language and variant from automation. A lock clears when you delete the manual files, when Sonarr or Radarr replaces or deletes the video, or when you release it with `subflux unlock` or `POST /api/search/clear-lock`.

## Timing

Downloaded subtitles rarely match your exact file, so subflux re-times each download before it reaches the disk. The alignment builds on [alass](https://github.com/kaegi/alass):

- It compares the download's dialogue with a text subtitle track inside the video, which may be in another language, and finds the offset and any frame-rate difference between them.
- Audio timing compares a voice-activity detector's output with the subtitle's dialogue. The detector is ported from WebRTC and re-tuned for film audio. It runs only when `timing.audio_fallback` is on and the subtitle tracks cannot verify the timing.

A timing subflux is confident of is applied and marked verified. A download that fits the video's track too poorly to be the same cut is refused, and the search moves to the next result. One that fits a little better, but not well enough to verify, is saved as it arrived and marked "Timing unverified". The same happens when the video has no usable subtitle track and audio timing is off. Audio timing and manual offsets are always available from the timing dialog on the web page.

Downloads matched by file hash or from the same release skip timing, because their timing is already right. Forced subtitles skip it too, because they have too few lines to align. `timing.sync` turns timing off for every download.

## Clean-up

Every saved file has HTML tags and extra whitespace removed and gets standard line endings. A file is converted to UTF-8 from the encoding subflux detects, with the subtitle's language as a hint, and a byte order mark is dropped. A file in an encoding subflux cannot name keeps its bytes unchanged. Hearing-impaired annotations are removed only when `post_processing.strip_hi` is on. The `post_processing` section in [Configuration](configuration.md) turns each step on or off.

When subflux saves or removes a series' or movie's subtitle files, it asks its Sonarr or Radarr instance to rescan it. The instance then shows the current files. When the instance cannot be reached, subflux sends the rescan once it answers again.

## The web page

The web page is served by the same program and updates live as subflux works. It is written in TypeScript with no framework, loads as native ES modules and receives updates over server-sent events.

- The coverage table lists every show and movie against your language rules, with have and total counts per language, embedded-track counts, a missing-only filter and a text search.
- The timing editor streams the video at 360p, using the bundled FFmpeg, with the subtitle as a live caption track. Move the offset with a timecode control and the captions reload in place. You can also run any timing method and preview its result before anything is written.
- Manual search queries every site for an item, shows each result's fit class with the facts behind it and why automation would skip it, and lets you download a specific result.
- The settings dialog renders every setting with a tooltip. A save is checked before it is applied and takes effect without a restart.
- History lists every download and search, filtered by type, language and site.

Until a valid configuration is saved, the settings dialog opens on its own and the page offers nothing else.

## Rejected credentials

When a site rejects its credentials, such as a wrong password, passkey or API key, subflux stops calling it instead of retrying until the site locks the account. The first rejection pauses the site for 5 minutes and the second for 30 minutes. Each pause ends with one test request, and the third rejection disables the site.

A disabled site raises an alert on the web page that stays until it is cleared, logs one ERROR line, sets `subflux_provider_health{provider,health="disabled"}` to 1 and fires the `SubfluxProviderUnusable` alert rule after 65 minutes. The disable survives a restart, and nothing turns the site back on by itself. Two things do:

- Save the site with different credentials.
- Press the site's **Test** button in the settings dialog. A pass with the recorded credentials turns the site back on. A pass with edited credentials asks you to save them.

Switching the site off clears its alert, and switching it back on with the same credentials disables it again. A rate-limit answer is not a rejection. It pauses only the operation that hit it, a search or a download, for as long as the site's `Retry-After` header says, or 10 minutes.

Saving settings restarts search with them. A search in progress stops and runs again under the new settings. A manual download in progress ends as interrupted, so start it again.

## Media folders that cannot be written

subflux saves subtitles next to the media, so a read-only media folder prevents any subtitle from being saved. It checks each folder by writing and deleting a small hidden file:

- in each media root before a full scan,
- in the folder a single-item scan, an import or a manual download writes into, before it starts,
- in a folder where a save just failed,
- every 5 minutes in a folder found unwritable.

When subtitles cannot be written in a folder, subflux stops searching for and downloading subtitles there. It shows an alert on the web page naming the folder and the error. It resumes once a write test there succeeds. subflux never changes file or folder permissions. New subtitle files get the permissions your share's umask and ACLs give them.

While such a folder stays unwritable, later imports from the same Sonarr or Radarr instance also wait, whatever folder they land in. That includes the other imports reported in the same check. None is lost. They are processed in order once the folder recovers, and the next full scan covers healthy folders meanwhile. A waiting import is also released once it no longer needs a subtitle there. That happens when you tag its series with one of your `search.exclude_arr_tags`, or when you delete its video. subflux checks it again at most 5 minutes plus one `poll_interval` after its last check.

## Missing and unmounted media

A video counts as deleted only when it is missing from a media root that is present, readable and not empty. An unmounted share usually looks like an empty root. If the root is missing or empty, or a file check fails or takes over 10 seconds, subflux keeps the subtitle records and manual locks. It also holds new imports there, shows an alert naming the path and checks again every 5 minutes. The `SubfluxMediaUnavailable` alert rule fires once that has lasted 5 minutes.

Give each mounted share its own media root, as its own root folder in Sonarr or Radarr or its own `sources[].path_mappings` entry, so an unmounted one shows up as an empty root.

## Resource use

subflux is one static Go program with no Python and no runtime dependencies, on a distroless base image. Sonarr and Radarr answers are fetched in full and then processed, worker pools have fixed sizes and media files are read as a stream. The largest answer measured, 4,360 movies, decodes to 24 MB.

The bundled FFmpeg is about 5 MB, plus about 2 MB for ffprobe. It holds decoders for the common video and audio codecs and for SRT, ASS, MOV text, WebVTT, PGS, DVD and DVB subtitles, one x264 encoder for the 360p preview, and no network support. It detects tracks, extracts subtitles and audio and streams the preview.

All state is one file, `/config/subflux.bolt`, kept with bbolt, a database written in Go. A write is safe on disk once it returns. Scheduled backups are available in the `backup` section.

## Limits

- Sites behind Cloudflare protection, such as subf2m, AvistaZ and CinemaZ, are not supported.
- An AnimeTosho result is placed on an episode by animap's anime map, so a wrong or missing mapping there gives a wrong or missing subtitle.
