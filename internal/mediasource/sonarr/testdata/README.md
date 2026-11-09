# Recorded Sonarr answers

Each directory holds the answers one live Sonarr gave to arrapi v3's reads, recorded on 2026-11-07:

- `v5-api-v5/`: Sonarr 5.0.0.2941, API v5.
- `v4-api-v3/`: Sonarr 4.0.20.3014, API v3.

`answers.json` maps each request's path and query to its file, status and content type; `arrtest.Server` serves them, stamped with the version `system-status.json` names.

## How they were recorded

A recorder drove arrapi's `sonarr` client through an `http.RoundTripper` that refused every method but GET and wrote each answer's body unchanged. It read `/api`, the system status, the series list, the episodes with their files of three series, one series by id, a series id the server does not have (the 404), the tags, the root folders and the history since a recent date.

The three series cover the shapes the adapter must handle:

- Firefly, in DVD order on Sonarr v5 and in aired order on Sonarr v4.
- Monogatari, an anime in an alternate (book) order on Sonarr v5.
- Shoah, whose files each hold two episodes, with unaired specials that have no file.

## How they were scrubbed

Before saving, a script cut each answer to those three series and:

- dropped every `overview`;
- renamed the tags to neutral labels, and the root folders' unmapped folders to placeholders;
- kept six history records on Sonarr v5 and three on Sonarr v4, each rewritten onto one of the three series: its release name, file paths and release group made up in Sonarr's own shapes (`GROUP1` and on), and its download ids, indexer names, guids and download-client names replaced with numbered placeholders;
- refused any history record naming another series, any file naming another series or folder of the recorded library, and any file holding an API key, a passkey or a host name of the recording network.

`TestFixturesHoldNoSecret` repeats the credential check on every file, and `TestHistoryNamesRecordedRoots` checks that every history record names one of the three series.
