# TsukiHime recorded answers

Read-only GETs to `api.tsukihime.org` and `storage.tsukihime.org`, no account. `a.json`, `e.json` and `one.json` are the 2026-11-04 answers; everything else was recorded on 2026-11-12 08:43 to 09:20 UTC, when `one.json` answered byte for byte as on 2026-11-04.

| File | Request |
|---|---|
| `openapi.json` | `/openapi.json`, the document the Swagger UI `https://api.tsukihime.org/docs` serves (OpenAPI 3.1, "Tsukihime beta-api 2.0.0") |
| `a.json` | `/v1/animes/anidb/17617` (anime 19) |
| `e.json` | `/v1/animes/19/episodes/1`: one torrent, `filecount` 0 |
| `a5.json` | `/v1/animes/anidb/18563` (anime 9823) |
| `e5.json` | `/v1/animes/9823/episodes/5` |
| `one.json` | `/v1/torrents/37747` |
| `torrent-<id>.json` | `/v1/torrents/<id>` |
| `335942.xz` | `storage.tsukihime.org/attach/00052046/335942.xz`: 200 `application/x-xz`, no redirect |
| `a-batch.json`, `torrent-367.json` | `/v1/animes/anidb/18886` (anime 284) and its 10-file batch `/v1/torrents/367` |
| `a-film.json` | `/v1/animes/anidb/18890` (anime 9627, `is_movie` 1) |
| `e-film.json` | `/v1/animes/9627/episodes/1`: `total` 0, since a film's torrents carry `episode_no` null |
| `torrents-film.json` | `/v1/torrents?anime_id=9627`: its 8 torrents |
| `a-unknown.json` | `/v1/animes/anidb/99999999`: 404 |

`e5.json` drops torrents 37823 and 37807, added after 1793801665 (2026-11-04 14:14:25 UTC, 37779's), which would push 37747 past a target-`en` search's 6 torrents; its `total` reads 13 to match. 37851, also newer, is kept: its only English label is `en-US`, so it is read after the `en` torrents and pushes nothing.

The `sublang` query filter matches one label exactly: `/v1/torrents?anime_id=9823&sublang=en` answered 90 of the anime's 115 torrents, dropping every `en-US`-only one, and `sublang=en,th` answered as `sublang=en`. The adapter never sends it.
