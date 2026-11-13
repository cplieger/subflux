# Does `episodes/{num}` answer specials?

No (2026-11-12). `/v1/animes/19/episodes/0` answers `total` 0, `/v1/animes/19/episodes/S1` answers 400 `{"detail": "Invalid episode_num format"}`, and the OpenAPI document gives no other route by episode. `{num}` is the anime's regular episode number (`episode_no` on each torrent), so a special landing asking it would get the regular episode of the same number: the adapter asks only for regular landings.
