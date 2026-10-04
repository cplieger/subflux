# Database maintenance

This page covers recovering, compacting and cleaning up around the subflux database, `/config/subflux.bolt`, for readers whose container fails on a damaged file or whose database has grown large. The commands use `/opt/appdata/subflux`, the host folder the example `compose.yaml` mounts at `/config`. Use your own folder if it differs.

## Recovering a damaged database

A damaged database makes the container restart in a loop, with one of these errors in `docker logs subflux`:

- `invalid database`
- `checksum mismatch`
- `meta page invalid`

To recover:

1. Stop the container with `docker compose stop subflux`.
2. Move the damaged file aside with `mv /opt/appdata/subflux/subflux.bolt /opt/appdata/subflux/subflux.bolt.corrupt`.
3. Start the container. It creates a new, empty database.
4. Wait for one full scan, which starts 30 seconds after the container starts. It rebuilds the existing subtitles, the subtitle file list and the scan records from the files on disk. The search history starts empty, so every item can be searched at once.
5. Open the web page and create the admin account again. Users, passkeys and API keys are lost, so the server is back in its first-start state. You can also turn on OIDC in `config.yaml` to sign in without a local account.
6. Set any manual locks again.

`subflux reset-password` cannot replace step 5. It changes the password of an existing user and answers 404 for an unknown name, so it cannot create the first account.

If you turned on backups, you can instead stop the container and copy the newest `subflux-<timestamp>.bolt` from the backup folder over `subflux.bolt`. That keeps users, locks and timing offsets as they were at the time of the copy.

Recovery loses:

- local users, passkeys and API keys,
- manual locks and the manual download history,
- timing offsets, which go back to zero,
- the search backoff state, so every site can be asked again at once.

Recovery rebuilds on its own:

- the subtitle file list and coverage,
- the record of automatic downloads, found during the first scan,
- the scan records and the import cursors, set again by the first scan and the first import check.

## Compacting the database

The database reuses its free space but never shrinks its file. Compaction is worth it when either holds:

- `subflux_store_freelist_bytes` is more than about half of `subflux_store_file_bytes`.
- The file is over 100 MB, which is large for subtitle records.

Normal use rarely needs it. It becomes useful after sustained heavy churn, such as repeated full rescans or a large library reorganization.

The `bbolt` tool is not in the image, which holds a single program and no shell. Run it on the host against the mounted folder, either with `go install go.etcd.io/bbolt/cmd/bbolt@latest` or in a throwaway container as shown in step 2.

1. Stop the container with `docker compose stop subflux`. bbolt holds an exclusive lock on the file while subflux runs.
2. Compact the file:

   ```sh
   docker run --rm -v /opt/appdata/subflux:/config golang:alpine sh -c \
     'go install go.etcd.io/bbolt/cmd/bbolt@latest && \
      bbolt compact -o /config/subflux-compact.bolt /config/subflux.bolt && \
      bbolt check /config/subflux-compact.bolt'
   ```

3. Make sure `bbolt check` reports no errors.
4. Give the new file the owner of the original with `sudo chown --reference=/opt/appdata/subflux/subflux.bolt /opt/appdata/subflux/subflux-compact.bolt`. The throwaway container runs as root, so the new file belongs to root and subflux cannot open it.
5. Replace the original with `mv /opt/appdata/subflux/subflux-compact.bolt /opt/appdata/subflux/subflux.bolt`.
6. Start the container.

Compaction rewrites the file in order and keeps only live data.

## Removing unused database files

subflux keeps its data in `/config/subflux.bolt` and its backups as `subflux-<timestamp>.bolt`. It never reads, prunes or deletes files named `subflux.db`, `subflux.db-wal`, `subflux.db-shm` or `subflux-<timestamp>.db`. If your settings folder holds any of them, they are unused and only take up space.

To remove them:

1. Stop the container with `docker compose stop subflux`.
2. Delete them with `sudo rm -f /opt/appdata/subflux/subflux.db /opt/appdata/subflux/subflux.db-wal /opt/appdata/subflux/subflux.db-shm /opt/appdata/subflux/subflux-*.db`.
3. If `backup.path` points to another folder, delete `subflux-*.db` there too.
4. Start the container.

## Checking the host kernel

ext4 with the `fast_commit` feature had a bug that could damage memory-mapped files on an unclean shutdown. bbolt maps its file into memory for reads, so the subflux database is exposed to it. The fix is in Linux 5.10.94 and later on the 5.10 branch, and 5.15.17 and later on the 5.15 branch.

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

After the next start, `docker logs subflux` shows no `invalid database` or `checksum` errors in its first lines when the database opened cleanly.
