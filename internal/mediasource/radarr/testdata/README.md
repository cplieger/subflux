# Recorded Radarr answers

`api-v3/` holds the answers one live Radarr 6.4.4.10685 (API v3) gave to arrapi v3's reads, recorded on 2026-11-07. `answers.json` maps each request's path and query to its file, status and content type; `arrtest.Server` serves them, stamped with the version `system-status.json` names.

## How they were recorded

A recorder drove arrapi's `radarr` client through an `http.RoundTripper` that refused every method but GET and wrote each answer's body unchanged. It read `/api`, the system status, the movie list, three movies by id, a movie id the server does not have (the 404), the tags, the root folders and the history since a recent date.

The three movies cover the shapes the adapter must handle:

- My Neighbor Totoro, Japanese, with nine audio tracks in seven languages.
- Scrapper, tagged, with a file.
- Fast Forever, not yet released, with no file.

## How they were scrubbed

Before saving, a script cut each answer to those three movies and:

- dropped every `overview`;
- renamed the tags to neutral labels;
- kept six history records, each rewritten onto one of the three movies: its release name, file paths and release group made up in Radarr's own shapes (`GROUP1` and on), and its download ids, indexer names, guids and download-client names replaced with numbered placeholders;
- refused any history record naming another movie, any file naming another movie or folder of the recorded library, and any file holding an API key, a passkey or a host name of the recording network.

`TestFixturesHoldNoSecret` repeats the credential check on every file, and `TestHistoryNamesRecordedRoots` checks that every history record names one of the three movies.
