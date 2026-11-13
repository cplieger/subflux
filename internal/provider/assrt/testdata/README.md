# Assrt recorded answers

Read-only GETs to `api.assrt.net` and `file0.assrt.net` on 2026-11-12 between 14:55 and 15:25 UTC, with an account's token as `Authorization: Bearer` on the API and none on downloads. No answer echoed the token. Each download link's `_` and `-` signature parameters are replaced with fixed values.

| File | Request |
|---|---|
| `search-inception.json` | `/v1/sub/search?q=Inception.2010.1080p.BluRay.x264-SPARKS&is_file=1&cnt=15&filelist=1`: 15 hits |
| `search.json` | `/v1/sub/search?q=Breaking.Bad.S01E03.720p.BluRay.x264-DEMAND&is_file=1&cnt=15&filelist=1`: 11 hits |
| `detail-626317.json` | `/v1/sub/detail?id=626317`: a rar of 2 files |
| `detail-95007.json` | `/v1/sub/detail?id=95007`: a rar of 10 files |
| `dl-626317.rar` | `detail-626317.json`'s `url` over https: 200 `application/octet-stream`, no redirect |
| `quota.json` | `/v1/user/quota` |
| `status-*.json` | the statuses `status.md` records |

What the capture showed:

- `user/quota` answers the requests left in a bucket that refills at the account's quota a minute (5 on this account): 5 at rest, 1 after seven back-to-back searches, 2 fifteen seconds later, 5 a minute later. Reading it spends nothing (5, 5, 5 back to back), so the pacer reads it unpaced, and a read a window after the last request reports the quota.
- With `filelist=1` every hit is in the documented shape and lists its archive members, with no links. An empty filelist answers `{}`, not `[]`. A hit with no `filename` (714169) cannot be downloaded.
- A detail's `url` is `http://file0.assrt.net/download/<id>/<name>?...`, and the archive answers over https. Its `filelist[].url` links (`/onthefly/...`) answer 302 to an error page on `assrt.net`, so a member is read from the archive.
- Member file names in a filelist match the archive's member names exactly (`dl-626317.rar`).
- Members name their language in their last segment (`.chs`, `.gb`, `.cht`, `.big5`, `.eng`), or in Chinese words (`简体`, `英文`), or not at all; a hit's `langlist` also holds `langdou`, bilingual, which names no one language.
