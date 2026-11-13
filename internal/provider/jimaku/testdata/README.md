# Jimaku recorded answers

Read-only GETs to `jimaku.cc` on 2026-11-12 between 13:04 and 13:20 UTC, each search and file list with an account's API key in `Authorization`, each download with none, spaced 2 to 3 seconds apart. No answer echoed the key.

| File | Request |
|---|---|
| `openapi.json` | `/api/openapi.json`, the document `https://jimaku.cc/api/docs` renders (OpenAPI 3.1.0, version "beta") |
| `search-tv-6484.json` | `/api/entries/search?anime=false&tmdb_id=tv:6484`: entry 4464, a TV drama |
| `search-movie-403579.json` | `/api/entries/search?anime=false&tmdb_id=movie:403579`: entry 7742, a film |
| `search-tv-61222-nonanime.json` | `/api/entries/search?anime=false&tmdb_id=tv:61222`: `[]` |
| `search-tv-61222-anime.json` | `/api/entries/search?anime=true&tmdb_id=tv:61222`: entry 5992, an anime-flagged series |
| `search-1563.json` | entry 1563's row from `/api/entries/search?query=one%20piece` |
| `search-401.json` | `/api/entries/search?tmdb_id=tv:209867` with a wrong key, and with none: both 401 `{"error":"unauthorized","code":7}` |
| `files-4464.json`, `files-7742.json`, `files-5992.json` | `/api/entries/<id>/files` |
| `files-1563-subset.json` | 7 of the 2,153 rows `/api/entries/1563/files` answered, in its order: two `.srt`, one `.ass`, the two packs below, one `.7z` and one `.sup` |
| `dl-4464-ep01.srt` | `files-4464.json`'s first `url`: 200 `application/x-subrip`, no redirect |
| `pack-442-443.zip` | that file list's `[HorribleSubs] One Piece 442-443 Retimed subs.zip`: 200 `application/zip` |
| `pack-207-219.rar` | its `[Netflix] One Piece 207-219.rar`: 200 `application/x-rar-compressed` |

What the capture showed:

- The search's `anime` filter defaults to true and splits Jimaku's entries: `tmdb_id=tv:6484` answered `[]` without `anime=false` and entry 4464 with it, and `tv:61222` the reverse. Of the 12,503 entries `anime=false` and `anime=true` listed, 7,545 of the 7,871 others carry a TMDB id and 21 of the 4,632 anime do (the rest carry an AniList id), and no TMDB id names two entries.
- Every file `url` is `https://jimaku.cc/entry/<id>/download/<escaped name>` (6,893 rows over the 35 entries' lists the capture read), at most 661 bytes. Downloads answer the same bytes with or without the key.
- File lists also hold `.7z` packs and `.sup` image subtitles, which subflux cannot read. One list can be long: entry 1563 answered 2,153 rows.
- The files `episode` filter is the site's guess from file names, so the adapter reads every file and leaves each name to the engine.
- No 429 was provoked. The quirk test's headers follow the documentation's rate-limit section (`x-ratelimit-limit`, `x-ratelimit-remaining`, `x-ratelimit-reset`, `x-ratelimit-reset-after`); every recorded answer carried the first three, `x-ratelimit-limit: 25`.
