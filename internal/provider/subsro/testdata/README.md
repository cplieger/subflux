# Subs.ro recorded answers

Read-only GETs to `api.subs.ro` on 2026-11-12 between 14:26 and 14:32 UTC, with an account's API key in `X-Subs-Api-Key` unless a row says otherwise, 2 seconds apart. No answer echoed the key.

| File | Request |
|---|---|
| `openapi.yaml` | the OpenAPI 3.0.0 document `https://subs.ro/api` embeds (its "Descarcă YAML" link answered 404) |
| `search-imdbid-tt1375666.json` | `/v1.0/search/imdbid/tt1375666`: a film, 3 items |
| `search-imdbid-tt0903747.json` | `/v1.0/search/imdbid/tt0903747`: a series, 15 items |
| `search-imdbid-tt0000001.json` | `/v1.0/search/imdbid/tt0000001`: 2 items, one of another film |
| `search-imdbid-bad.json` | `/v1.0/search/imdbid/abc`: 400 "Invalid IMDB ID format" |
| `search-403.json` | `/v1.0/search/imdbid/tt1375666` with a wrong key: 403 "Invalid API key" |
| `dl-50829.zip` | `/v1.0/subtitle/50829/download`: 200 `application/octet-stream`, a zip, no redirect |
| `dl-80010.rar` | `/v1.0/subtitle/80010/download`: a rar of season 1's episodes |
| `dl-404.json` | `/v1.0/subtitle/999999999/download`: 404 "Subtitle not found" |
| `quota.json` | `/v1.0/quota` |
| `quota-403.json`, `quota-401.json` | `/v1.0/quota` with a wrong key and with none: 403 "Invalid API key", 401 "Missing API key" |

What the capture showed:

- The key's quota is 300 requests a day. Searches, details and downloads each spend one; `/quota` spends none (`used` read 2 after two searches and three quota requests), so the connection test asks it.
- Every `downloadLink` is `https://api.subs.ro/v1.0/subtitle/<id>/download`, and every download is a zip or rar archive answered directly. The adapter builds that path from the id and never reads `downloadLink`.
- Items carry `language` `ro`, `en`, `fra` and `spa`; the OpenAPI document lists `ro`, `en`, `ita`, `fra`, `ger`, `ung`, `gre`, `por`, `spa` and `alt` (none named).
- A series' Romanian items are season packs titled `<show> - Sezonul <n>`; other items' descriptions often list their releases after `Release(s):`, comma separated, or read `Release(s): None`.
- Subs.ro files items under a wrong IMDb id: `tt0000001` (Carmencita, 1894) also answers "End of Liberty", year 2010. Each candidate carries the item's year, so the engine's year check drops it.
- The search answered `tt1375666` and `1375666` alike, and paged nothing: `count` equalled the items listed (29 for `tt0944947`).
- No 429 was provoked; the documentation states a daily quota and no reset time.
