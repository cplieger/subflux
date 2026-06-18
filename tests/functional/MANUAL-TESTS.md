# Subflux Manual Test Suite

Tests that require browser interaction, visual verification, or real-time
observation. Run these after the automated suite (`run.sh`) passes.

## Prerequisites

- Subflux running with real Sonarr/Radarr credentials
- At least one series and one movie with files on disk
- At least one previously downloaded subtitle (for sync/preview tests)
- Browser open to the subflux web UI
- For auth tests: auth enabled (not `disable_auth: true`)

---

## 1. Web UI Navigation & SPA Routing

| #    | Step                                 | Expected                                       |
| ---- | ------------------------------------ | ---------------------------------------------- |
| 1.1  | Open `/`                             | Library page loads, series/movies tabs visible |
| 1.2  | Click series tab                     | Series list loads with coverage badges         |
| 1.3  | Click a series                       | Episode list with per-episode coverage         |
| 1.4  | Click movies tab                     | Movie list loads with coverage badges          |
| 1.5  | Click History in nav                 | History page with download records             |
| 1.6  | Click Settings in nav                | Settings form loads with all sections          |
| 1.7  | Refresh browser on `/library/series` | SPA routing: page loads correctly (not 404)    |
| 1.8  | Refresh browser on `/settings`       | Settings page loads correctly                  |
| 1.9  | Use browser back/forward buttons     | Navigation works, URL updates                  |
| 1.10 | Open `/login` directly               | Login page renders (standalone HTML)           |

## 2. Theme Switching

| #   | Step                                | Expected                          |
| --- | ----------------------------------- | --------------------------------- |
| 2.1 | Switch to dark theme via user menu  | Colors change, no layout breakage |
| 2.2 | Switch to light theme via user menu | Colors change, text readable      |
| 2.3 | Refresh page                        | Theme persists across reload      |
| 2.4 | Login page follows system theme     | Matches OS dark/light preference  |

## 3. Settings UI (Schema-Driven Forms)

| #    | Step                                             | Expected                                                                                                                                   |
| ---- | ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------ |
| 3.1  | Open Settings                                    | All sections render: Sonarr, Radarr, Media Roots, Polling, Languages, Providers, Search, Adaptive, Post Processing, Scoring, Logging, Auth |
| 3.2  | Toggle Sonarr enabled off                        | Dependent fields gray out or hide                                                                                                          |
| 3.3  | Toggle Sonarr enabled on                         | Fields re-enable                                                                                                                           |
| 3.4  | Change poll interval to `60s`                    | Saves successfully, parsed config shows 60s                                                                                                |
| 3.5  | Enter invalid poll interval `abc`                | Validation error shown                                                                                                                     |
| 3.6  | Add a language rule: audio `de` -> subtitle `en` | Rule appears in list                                                                                                                       |
| 3.7  | Add variant `forced` to a language target        | Variant selector works                                                                                                                     |
| 3.8  | Remove a language rule                           | Rule disappears                                                                                                                            |
| 3.9  | Enable mock provider                             | Provider section expands with mock settings                                                                                                |
| 3.10 | Change mock mode to `error`                      | Saves successfully                                                                                                                         |
| 3.11 | Toggle `audio_sync_fallback` on                  | `requires` field: only enabled when `sync_subtitles` is on                                                                                 |
| 3.12 | Toggle `sync_subtitles` off                      | `audio_sync_fallback` disables automatically                                                                                               |
| 3.13 | Click Reset to Defaults                          | Confirmation dialog, config resets                                                                                                         |
| 3.14 | Save with no arr endpoints                       | Validation error: at least one arr required                                                                                                |
| 3.15 | Save with no languages                           | Validation error: at least one language required                                                                                           |
| 3.16 | Save with no enabled providers                   | Validation error: at least one provider required                                                                                           |

## 4. Authentication & Login

| #   | Step                              | Expected                                             |
| --- | --------------------------------- | ---------------------------------------------------- |
| 4.1 | Open app with no users created    | Setup wizard shown                                   |
| 4.2 | Create admin account via wizard   | Account created, auto-logged in                      |
| 4.3 | Log out                           | Redirected to login page                             |
| 4.4 | Log in with correct credentials   | Redirected to library                                |
| 4.5 | Log in with wrong password        | "Invalid credentials" error, no account enumeration  |
| 4.6 | Log in with nonexistent username  | Same error as wrong password (timing equalization)   |
| 4.7 | Attempt 10+ rapid logins          | Rate limited (429), Retry-After header shown         |
| 4.8 | Login page shows passkey autofill | If WebAuthn configured, conditional mediation active |

## 5. TOTP Two-Factor Authentication

| #    | Step                               | Expected                             |
| ---- | ---------------------------------- | ------------------------------------ |
| 5.1  | Open Security page from user menu  | Security settings visible            |
| 5.2  | Click Enable TOTP                  | QR code / secret URI displayed       |
| 5.3  | Scan QR with authenticator app     | App shows 6-digit code               |
| 5.4  | Enter valid code to confirm        | TOTP enabled, recovery codes shown   |
| 5.5  | Copy recovery codes                | Codes saved (shown only once)        |
| 5.6  | Log out and log in                 | Password accepted, TOTP prompt shown |
| 5.7  | Enter valid TOTP code              | Login completes                      |
| 5.8  | Enter invalid TOTP code            | Rejected, can retry                  |
| 5.9  | Enter same TOTP code twice in 30s  | Replay detected, rejected            |
| 5.10 | Enter a recovery code instead      | Login completes, code consumed       |
| 5.11 | Disable TOTP (requires valid code) | TOTP disabled, no longer prompted    |

## 6. Passkey (WebAuthn) Authentication

| #   | Step                           | Expected                                            |
| --- | ------------------------------ | --------------------------------------------------- |
| 6.1 | Open Security page             | Passkey section visible                             |
| 6.2 | Click Register Passkey         | Browser credential prompt appears                   |
| 6.3 | Complete registration          | Passkey listed with friendly name                   |
| 6.4 | Log out and log in via passkey | Passkey autofill or button works                    |
| 6.5 | Rename a passkey               | Name updates in list                                |
| 6.6 | Delete a passkey               | Passkey removed (reauth required)                   |
| 6.7 | Try to delete last auth method | Blocked: "cannot remove last authentication method" |

## 7. API Keys

| #   | Step                                | Expected                          |
| --- | ----------------------------------- | --------------------------------- |
| 7.1 | Open Security page                  | API Keys section visible          |
| 7.2 | Generate new API key                | Key shown once with prefix/suffix |
| 7.3 | Copy the key                        | Full key available for copy       |
| 7.4 | Use key via `X-API-Key` header      | API access works                  |
| 7.5 | Use key via `?api_key=` query param | API access works                  |
| 7.6 | Revoke the key (reauth required)    | Key removed, access denied        |

## 8. Re-authentication for Sensitive Operations

| #   | Step                                  | Expected                  |
| --- | ------------------------------------- | ------------------------- |
| 8.1 | Try to delete a passkey               | Reauth dialog appears     |
| 8.2 | Enter password in reauth dialog       | Operation proceeds        |
| 8.3 | Try another sensitive op within 5 min | No reauth needed (cached) |
| 8.4 | Wait 5+ minutes, try again            | Reauth dialog reappears   |
| 8.5 | Try to generate API key               | Reauth required           |
| 8.6 | Try to disable TOTP                   | Reauth required           |
| 8.7 | Try to revoke API key                 | Reauth required           |

## 9. Admin User Management

| #   | Step                            | Expected                                  |
| --- | ------------------------------- | ----------------------------------------- |
| 9.1 | Open Security page as admin     | Users section visible                     |
| 9.2 | Create a new user (role: user)  | User appears in list                      |
| 9.3 | Create a new user (role: admin) | User appears with admin badge             |
| 9.4 | Delete a user                   | User removed, cascades sessions/keys      |
| 9.5 | Try to delete yourself          | Blocked: "cannot delete your own account" |
| 9.6 | Log in as non-admin user        | Users section not visible                 |

## 10. Manual Search (Browser)

| #    | Step                                        | Expected                                               |
| ---- | ------------------------------------------- | ------------------------------------------------------ |
| 10.1 | Open a movie detail page                    | Search button visible                                  |
| 10.2 | Click search for English subtitles          | Results load with provider, score, release name        |
| 10.3 | Results show score tier badges              | Excellent/good/acceptable/minimal colored correctly    |
| 10.4 | Results show "already downloaded" indicator | If a subtitle was previously downloaded for this media |
| 10.5 | Click download on a result                  | Download starts, activity log updates                  |
| 10.6 | After download, coverage badge updates      | SSE event triggers UI refresh                          |
| 10.7 | Download same media+language again          | Creates manual lock, shows lock indicator              |
| 10.8 | Search for a series episode                 | Results include season pack entries (if available)     |
| 10.9 | Search with no results                      | "No results" message shown                             |

## 11. Manual Download & Lock Flow

| #    | Step                                       | Expected                                  |
| ---- | ------------------------------------------ | ----------------------------------------- |
| 11.1 | Manually download a subtitle for a movie   | File appears on disk, state updated       |
| 11.2 | Check locks page                           | Media+language pair shows as locked       |
| 11.3 | Trigger a scan for that movie              | Scan skips the locked language            |
| 11.4 | Clear the lock via UI                      | Lock removed, scan would now search       |
| 11.5 | Delete the manually downloaded file via UI | File removed from disk, lock auto-cleared |

## 12. Subtitle Sync Dialog

| #     | Step                                       | Expected                                               |
| ----- | ------------------------------------------ | ------------------------------------------------------ |
| 12.1  | Open sync dialog for a downloaded subtitle | Video preview loads (fMP4 or buffered)                 |
| 12.2  | Play/pause video preview                   | Controls work, subtitle overlay visible                |
| 12.3  | Seek to different position                 | Video jumps, subtitles update                          |
| 12.4  | Click +100ms offset button                 | Subtitle timing shifts forward                         |
| 12.5  | Click -100ms offset button                 | Subtitle timing shifts backward                        |
| 12.6  | Click +1s offset button                    | Larger shift applied                                   |
| 12.7  | Click -1ms offset button                   | Fine adjustment works                                  |
| 12.8  | Click Save offset                          | Offset persisted, file rewritten                       |
| 12.9  | Reopen sync dialog                         | Previous offset shown correctly                        |
| 12.10 | Click Audio Sync (dry run)                 | Shows predicted offset and confidence without applying |
| 12.11 | Click Audio Sync (apply)                   | Offset applied, file rewritten, offset shown           |
| 12.12 | Switch subtitle language in preview        | Different language track loads                         |

## 13. Season Batch Audio Sync

| #    | Step                                    | Expected                             |
| ---- | --------------------------------------- | ------------------------------------ |
| 13.1 | Open a series with downloaded subtitles | Season batch sync button visible     |
| 13.2 | Click batch audio sync for a season     | Progress indicator shows per-episode |
| 13.3 | Wait for completion                     | All episodes synced, offsets shown   |

## 14. Video Preview Edge Cases

| #    | Step                            | Expected                                      |
| ---- | ------------------------------- | --------------------------------------------- |
| 14.1 | Preview a 4K HDR video          | Transcoded to 360p, plays in browser          |
| 14.2 | Preview a video with no audio   | Preview still works (video only)              |
| 14.3 | Preview with Safari browser     | Buffered mode works (Content-Length required) |
| 14.4 | Preview a very long movie (>2h) | Seek works across full duration               |

## 15. SSE Real-Time Updates

| #    | Step                                  | Expected                                            |
| ---- | ------------------------------------- | --------------------------------------------------- |
| 15.1 | Open library page in browser          |                                                     |
| 15.2 | Trigger a scan from another tab/CLI   | Coverage badges update in real-time without refresh |
| 15.3 | Download a subtitle from another tab  | Coverage updates in the first tab                   |
| 15.4 | Disconnect network briefly, reconnect | SSE reconnects automatically                        |

## 16. Scan Behavior Observation

| #    | Step                                       | Expected                                         |
| ---- | ------------------------------------------ | ------------------------------------------------ |
| 16.1 | Trigger full scan                          | Activity log shows progress (current/total)      |
| 16.2 | Trigger full scan while one is running     | Returns 409 "scan already in progress"           |
| 16.3 | Trigger series scan during full scan       | Series scan queues behind full scan              |
| 16.4 | Watch scan delay between items             | Items processed with configured `scan_delay` gap |
| 16.5 | Scan with `exclude_arr_tags: [no-subflux]` | Tagged media skipped in scan                     |
| 16.6 | Scan recently scanned media                | Skipped if within `scan_interval`                |
| 16.7 | Cancel a queued scan via activity UI       | Scan removed from queue                          |

## 17. Poller (Sonarr/Radarr Import Detection)

| #    | Step                             | Expected                                      |
| ---- | -------------------------------- | --------------------------------------------- |
| 17.1 | Import a new episode in Sonarr   | Subflux detects import within `poll_interval` |
| 17.2 | Check activity log               | Shows "Import detected" entry                 |
| 17.3 | Verify subtitle search triggered | Search runs for the imported episode          |
| 17.4 | Import a new movie in Radarr     | Same detection and search flow                |

## 18. Adaptive Backoff Observation

| #    | Step                                    | Expected                                    |
| ---- | --------------------------------------- | ------------------------------------------- |
| 18.1 | Configure mock provider in `empty` mode |                                             |
| 18.2 | Scan a movie                            | No results found, backoff entry created     |
| 18.3 | Check `/api/backoff`                    | Entry shows with `next_retry` in the future |
| 18.4 | Scan same movie again immediately       | Provider skipped (backed off)               |
| 18.5 | Wait for backoff to expire, scan again  | Provider retried                            |
| 18.6 | Set `max_attempts: 1`, scan twice       | After 1 failure, permanently backed off     |

## 19. Provider Priority & Filtering

| #    | Step                                             | Expected                             |
| ---- | ------------------------------------------------ | ------------------------------------ |
| 19.1 | Enable mock (priority 1) + gestdown (priority 4) |                                      |
| 19.2 | Search for a TV episode                          | Both providers queried               |
| 19.3 | Configure language rule with `providers: [mock]` | Only mock queried for that language  |
| 19.4 | Configure language rule with `exclude: [mock]`   | Mock excluded, only gestdown queried |

## 20. File Manager

| #    | Step                                          | Expected                             |
| ---- | --------------------------------------------- | ------------------------------------ |
| 20.1 | Open file manager for a media item            | Lists embedded + external subtitles  |
| 20.2 | External files show file size                 | Size column populated                |
| 20.3 | Embedded tracks show codec (subrip, ass, pgs) | Codec column populated               |
| 20.4 | Delete a single external subtitle             | File removed, coverage updates       |
| 20.5 | Bulk delete all subtitles for a media item    | All external files removed           |
| 20.6 | Verify embedded tracks not deletable          | No delete button on embedded entries |

## 21. History Page

| #    | Step                                 | Expected                                |
| ---- | ------------------------------------ | --------------------------------------- |
| 21.1 | Open history page                    | Download records listed with timestamps |
| 21.2 | Click a media title link             | Navigates to library detail page        |
| 21.3 | Filter by media type (episode/movie) | List filters correctly                  |
| 21.4 | Filter by language                   | Only matching records shown             |
| 21.5 | Filter by provider                   | Only matching records shown             |
| 21.6 | Manual downloads show "manual" badge | Distinguishable from auto downloads     |

## 22. Coverage Badges

| #    | Step                                | Expected                           |
| ---- | ----------------------------------- | ---------------------------------- |
| 22.1 | Series with full coverage           | Green badge                        |
| 22.2 | Series with partial coverage        | Yellow/orange badge                |
| 22.3 | Series with no coverage             | Red or no badge                    |
| 22.4 | Movie with ignored codec only (PGS) | Yellow "ignored" badge (not green) |
| 22.5 | Episode with embedded + external    | Shows both sources                 |

## 23. Notification System

| #    | Step                                       | Expected                     |
| ---- | ------------------------------------------ | ---------------------------- |
| 23.1 | Trigger a successful download              | Success notification appears |
| 23.2 | Trigger a provider error (mock error mode) | Error notification appears   |
| 23.3 | Notification auto-dismisses                | Disappears after timeout     |
| 23.4 | Click dismiss on notification              | Immediately removed          |

## 24. Mobile Responsiveness

| #    | Step                               | Expected                                  |
| ---- | ---------------------------------- | ----------------------------------------- |
| 24.1 | Open UI on mobile viewport (375px) | Layout adapts, no horizontal scroll       |
| 24.2 | Navigate library on mobile         | Touch targets adequate, lists readable    |
| 24.3 | Open settings on mobile            | Form fields usable, sections collapsible  |
| 24.4 | Open sync dialog on mobile         | Video preview scales, controls accessible |
| 24.5 | Login page on mobile               | Form centered, usable                     |

## 25. Accessibility

| #    | Step                                 | Expected                                 |
| ---- | ------------------------------------ | ---------------------------------------- |
| 25.1 | Tab through all interactive elements | Focus order logical, focus ring visible  |
| 25.2 | Use keyboard to navigate library     | Enter opens items, Escape closes dialogs |
| 25.3 | Enable forced-colors mode            | UI remains usable                        |
| 25.4 | Enable prefers-reduced-motion        | Animations disabled                      |
| 25.5 | Enable prefers-contrast              | Contrast increases                       |
| 25.6 | Screen reader on library page        | Semantic structure, labels present       |
| 25.7 | Reauth dialog keyboard accessible    | Tab to password field, Enter to submit   |

## 26. Prometheus Metrics

| #    | Step                   | Expected                                      |
| ---- | ---------------------- | --------------------------------------------- |
| 26.1 | Curl `/metrics`        | Prometheus text format                        |
| 26.2 | Trigger a search       | `subflux_search_duration_seconds` incremented |
| 26.3 | Trigger a download     | `subflux_downloads_total` incremented         |
| 26.4 | Trigger a scan         | `subflux_scan_*` metrics updated              |
| 26.5 | Cause a provider error | `subflux_provider_failures_total` incremented |

## 27. CLI Subcommands

| #     | Step                                            | Expected                            |
| ----- | ----------------------------------------------- | ----------------------------------- |
| 27.1  | `docker exec subflux /subflux health`           | Exit 0 when healthy                 |
| 27.2  | `docker exec subflux /subflux status`           | Prints stats JSON                   |
| 27.3  | `docker exec subflux /subflux providers`        | Lists providers with status         |
| 27.4  | `docker exec subflux /subflux backoff`          | Shows backoff entries               |
| 27.5  | `docker exec subflux /subflux locks`            | Shows manual locks                  |
| 27.6  | `docker exec subflux /subflux timeouts`         | Shows provider timeout state        |
| 27.7  | `docker exec subflux /subflux timeouts-reset`   | Clears all timeouts                 |
| 27.8  | `docker exec subflux /subflux scan`             | Triggers full scan                  |
| 27.9  | `docker exec subflux /subflux reset-password`   | Resets admin password via stdin     |
| 27.10 | `docker exec subflux /subflux generate-api-key` | Generates API key, prints to stdout |
| 27.11 | `docker exec subflux /subflux unknown-cmd`      | Prints error and exits 1            |

## 28. Specials (Season 0)

| #    | Step                                        | Expected                            |
| ---- | ------------------------------------------- | ----------------------------------- |
| 28.1 | Find a series with specials (S00) in Sonarr |                                     |
| 28.2 | Open the series in subflux                  | Season 0 / Specials section visible |
| 28.3 | Search for subtitles for a special          | Search uses season=0, episode=N     |
| 28.4 | Scan the series                             | Specials included in scan           |

## 29. Multi-Episode Files

| #    | Step                                            | Expected                            |
| ---- | ----------------------------------------------- | ----------------------------------- |
| 29.1 | Find a multi-episode file (S01E01E02) in Sonarr |                                     |
| 29.2 | Scan the series                                 | File processed once, not duplicated |
| 29.3 | Subtitle saved with correct naming              | Matches the multi-episode file      |

## 30. Edge Case Media

| #    | Step                                             | Expected                                         |
| ---- | ------------------------------------------------ | ------------------------------------------------ |
| 30.1 | Movie with non-ASCII title (e.g. Japanese)       | Title displays correctly in UI and search        |
| 30.2 | Series with alternate titles                     | Search uses alternate titles for better matching |
| 30.3 | Movie with no IMDB ID                            | Search falls back to title+year                  |
| 30.4 | Episode with scene numbering different from TVDB | Scene numbers used in search                     |
| 30.5 | Very old movie (pre-1980)                        | Search works, year matching correct              |
| 30.6 | Recently released movie (this week)              | Search works, fewer results expected             |

## 31. Concurrent Operations

| #    | Step                                     | Expected                           |
| ---- | ---------------------------------------- | ---------------------------------- |
| 31.1 | Open two browser tabs to subflux         | Both work independently            |
| 31.2 | Trigger scan in tab 1, search in tab 2   | Both complete without interference |
| 31.3 | Save config while scan is running        | Hot reload applies, scan continues |
| 31.4 | Multiple manual downloads simultaneously | All complete, no file corruption   |

## 32. Error Recovery

| #    | Step                                   | Expected                                |
| ---- | -------------------------------------- | --------------------------------------- |
| 32.1 | Kill and restart subflux during a scan | Restarts cleanly, no DB corruption      |
| 32.2 | Make Sonarr unreachable, trigger scan  | Graceful error, scan skips Sonarr media |
| 32.3 | Make Radarr unreachable, trigger scan  | Graceful error, scan skips Radarr media |
| 32.4 | Fill disk, trigger download            | Error reported, no partial files left   |
| 32.5 | Corrupt the config file, restart       | Unconfigured mode, settings page shown  |

## 33. User Menu

| #    | Step                         | Expected                           |
| ---- | ---------------------------- | ---------------------------------- |
| 33.1 | Click user avatar in nav     | Popover menu opens                 |
| 33.2 | Menu shows username and role | Correct user info                  |
| 33.3 | Click Settings               | Navigates to settings              |
| 33.4 | Click Security               | Opens security panel               |
| 33.5 | Click Theme toggle           | Theme switches                     |
| 33.6 | Click Logout                 | Session ended, redirected to login |
| 33.7 | Click outside popover        | Menu closes                        |
