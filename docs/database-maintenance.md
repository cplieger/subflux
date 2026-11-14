# Database maintenance

This page covers what happens to the subflux database, `/config/subflux.bolt`, when its format changes, when the file is damaged and when it grows, for readers whose container stops at boot or whose database has grown large. The commands use `/opt/appdata/subflux`, the host folder the example `compose.yaml` mounts at `/config`. Use your own folder if it differs.

## When the format changes

A release that changes how subflux stores its data resets the database instead of converting it. At the first start of such a release, subflux:

1. keeps the whole old file beside the new one as `subflux.bolt.<8 hex digits>.<UTC time>.old`, such as `subflux.bolt.0a1b2c3d.20261103T120000.000000000Z.old`,
2. deletes all but the two newest `.old` copies,
3. recreates the parts of the database whose format changed, empty,
4. logs one `statedb: store format changed, reset` warning naming each recreated part and the kept copy.

The next scan refills the library from Sonarr, Radarr and the files on disk. A reset loses what only the database knew: manual locks, the download history, timing offsets and the job records. Settings live in `config.yaml`, outside the database, so no reset touches them. The first start after an upgrade from 0.9 resets the whole file, so its users, passkeys and API keys are not carried over.

The copy is complete before anything is recreated, and a reset that fails leaves the file as it was. The `.old` copies are only for you: subflux never reads them, so delete them once you no longer need them.

## When the database cannot be opened

Every failure to open the database stops the boot, with an `app: could not open the state store` error in `docker logs subflux`. The file is left exactly as it was.

- `statedb: the store is held by another process`: another program has the file open, such as a second subflux container on the same folder. subflux waits 5 seconds for it, then stops. Stop the other program and start the container again.
- `bolttable: invalid database file`: the file is damaged or is not a database. Recover it as below.

To recover a damaged file:

1. Stop the container with `docker compose stop subflux`.
2. Move the damaged file aside with `mv /opt/appdata/subflux/subflux.bolt /opt/appdata/subflux/subflux.bolt.corrupt`.
3. If you have a copy of the file from before the damage, copy it to `/opt/appdata/subflux/subflux.bolt`. Otherwise skip this step, and subflux starts with an empty database.
4. Start the container. An empty database is refilled by the next scan, and loses the same things a reset does.

## Compaction

The database reuses its free space but does not shrink its file on its own while running. At each start, subflux compacts the file when its free pages make up at least half of it and at least 64 MiB, and logs `statedb: store compacted`. A compaction that fails only logs a `statedb: store compaction failed` warning, and subflux starts on the uncompacted file. You never need to compact it by hand.

## Inspecting the database

`subflux store` asks the running subflux for the database's counts and sizes, through the admin socket inside the container. Run it with `docker exec subflux subflux store`. It needs the container running, since only the running process can open the file.

- `subflux store` reads the counts and sizes and changes nothing.
- `subflux store --verify` also rebuilds every index, counter and derived row (each item's subtitle coverage and the library counts) from the rows they come from, and lists each one that differs. It changes nothing, and on a large library it takes a while. It needs settings in force: with no `config.yaml`, or one that does not load, it fails with `statedb: no activation in force`.
- `subflux store --verify --repair` rewrites each index entry, counter and derived row that `--verify` found different. It never rewrites a primary row: a media item, a download, a lock or an account stays as stored. `--repair` without `--verify` is refused.

It prints one JSON object, holding no key, stored value or secret:

| Field | What it holds |
| --- | --- |
| `Namespaces` | each part of the database: its `Name`, its `Format` digest and its `Tables`, each with a `Name`, its row count `Rows` and the `Bytes` its pages use |
| `Mismatches` | with `--verify`, each table, index, counter or derived row that differs: its `Name`, the `Missing`, `Extra` and `Undecodable` entries, and `Repaired` when `--repair` rewrote it |
| `Verified` | whether `--verify` ran |
| `Rev`, `OldestRetained`, `ConfigRev` | the newest change revision, the oldest one still kept for live updates, and the revision of the settings in force |
| `Commits` | the write transactions committed since subflux started |
| `FileBytes`, `FreeBytes` | the file's size and its free space, which compaction returns |
| `Epoch` | a random identity for this run's open of the file, new at every start |

A row that cannot be decoded during `--verify` stops the command with a `statedb: inspect` error naming its table; recover the file as in [When the database cannot be opened](#when-the-database-cannot-be-opened).

## Removing unused database files

subflux keeps its data in `/config/subflux.bolt`, the lock file `subflux.bolt.lock` beside it, and the `.old` copies a reset keeps. It never reads, prunes or deletes files named `subflux.db`, `subflux.db-wal`, `subflux.db-shm` or `subflux-<timestamp>.db`. If your settings folder holds any of them, they are unused and only take up space.

To remove them:

1. Stop the container with `docker compose stop subflux`.
2. Delete them with `sudo rm -f /opt/appdata/subflux/subflux.db /opt/appdata/subflux/subflux.db-wal /opt/appdata/subflux/subflux.db-shm /opt/appdata/subflux/subflux-*.db`.
3. Start the container.

## Checking the host kernel

ext4 with the `fast_commit` feature had a bug that could damage memory-mapped files on an unclean shutdown. subflux maps its database file into memory for reads, so the file is exposed to it. The fix is in Linux 5.10.94 and later on the 5.10 branch, and 5.15.17 and later on the 5.15 branch.

Run `uname -r` on the host and compare the version with those two.

| Platform | Kernel | Status |
| --- | --- | --- |
| TrueNAS Community (ZFS) | 6.x | Not affected, ZFS is not ext4 |
| DietPi or Raspberry Pi | 6.x | Not affected, above the fixed versions |
| Synology DSM | 4.4.x | Not affected |

ZFS hosts are not affected, because ZFS uses its own copy-on-write transactions. TrueNAS and other ZFS-backed Docker hosts need no action.

On an affected ext4 host, do one of these:

- Unmount the filesystem and turn `fast_commit` off with `tune2fs -O ^fast_commit /dev/sdXn`.
- Upgrade the kernel to a fixed version.
