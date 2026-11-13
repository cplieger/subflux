# SubX recorded answers

Read-only GETs to `subx-api.duckdns.org` on 2026-11-12 between 18:26 and 18:40 UTC, with subflux's User-Agent and an account's API key as `Authorization: Bearer` unless a row says otherwise, 3 seconds apart. No answer echoed the key.

| File | Request |
|---|---|
| `robots-404.html` | `/robots.txt`, no key: 404, so every path is allowed (RFC 9309 2.3.1.3) |
| `search-imdb-tt1375666.json` | `/api/subtitles/search?imdb_id=tt1375666&video_type=movie&limit=200`: a film, 84 items |
| `search-imdb-tt0903747-s1.json` | `/api/subtitles/search?imdb_id=tt0903747&video_type=episode&season=1&limit=200`: a series' season 1, 112 items |
| `search-imdb-tt0903747-s1e3.json` | `/api/subtitles/search?imdb_id=tt0903747&video_type=episode&season=1&episode=3`: 16 items |
| `search-title-inception-2010.json` | `/api/subtitles/search?title=Inception&year=2010&video_type=movie&limit=200`: 91 items |
| `search-title-breakingbad-2008-s1.json` | `/api/subtitles/search?title=Breaking%20Bad&year=2008&video_type=episode&season=1&limit=200`: 126 items |
| `search-tester.json` | `/api/subtitles/search?imdb_id=tt1375666&video_type=movie&limit=1` |
| `search-401.json` | `/api/subtitles/search?imdb_id=tt1375666` with a wrong key: 401 "Invalid or expired token" |
| `dl-5bd14610.rar`, `dl-daedbf8a.rar` | `/api/subtitles/<id>/download`: 200 `application/octet-stream`, a rar, no redirect |
| `dl-e47eda2c.zip` | `/api/subtitles/<id>/download`: 200 `application/zip` |
| `dl-404.json` | `/api/subtitles/00000000-0000-4000-8000-000000000000/download`: 404 "Subtitle not found" |

What the capture showed:

- Every answer carries `X-RateLimit-Limit` and `X-RateLimit-Window: 60`: 250 searches and 500 downloads a minute on this key.
- Every item is Spanish and names no variety; its free-text `description` sometimes does ("Español Europeo", "Español de España", "español peninsular", "Español latino"). Uploaders also write "Castellano neutro" for neutral Spanish, so the word "castellano" alone does not name Spain's Spanish there.
- An `imdb_id` search answers a series' episodes too, each filed under the episode's own IMDb id (`tt1054725` for S01E03), and its season packs under the series' id with `episode` null. With no `limit` it answers at most 100 items (the series' unfiltered search stopped at 100, `total` 100), so `total` counts the answer, not the catalogue.
- `season` with `video_type=episode` answers the season's episodes and its packs; `episode` too drops the packs. `season` without `video_type=episode` answers 422.
- A title search also answers other titles' items (`Inception: The Cobol Job (2010)`, trailers) and items with no IMDb id or year.
- Titles read `<title> (<year>)` for a film and `<title> S01E03` or `<title> S01` for an episode or pack; descriptions often name the release as a dotted scene name (`Breaking.Bad.S01E03.DVDRip.XviD-ORPHEUS`).
- Item ids are lower-case UUIDs, and no answer names a download URL: the download path is built from the id and answers the uploaded file directly, a rar or a zip.
- An unknown or malformed id's download answers 404.
