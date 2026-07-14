# Operability Runbooks

Operational procedures for the bbolt store engine (`/config/subflux.bolt`).

## 1. Corrupt-file recovery

### Symptoms

Container crash-loops with errors like:

- `invalid database`
- `checksum mismatch`
- `meta page invalid`

in the container logs.

### Recovery procedure

1. Stop the container:

   ```sh
   docker compose stop subflux
   ```

2. Move the corrupt file aside:

   ```sh
   mv /config/subflux.bolt /config/subflux.bolt.corrupt
   ```

3. Restart the container. It creates a fresh empty store on startup.

4. Wait for one full scan cycle (runs after the 30s startup delay).
   Library state is rebuilt automatically from the filesystem:
   - `subtitle_state` (auto rows) - detected from on-disk subtitle files
   - `subtitle_files` - inventoried from media directories
   - `scan_state` - populated as each item is scanned
   - `search_attempts` - starts empty (all items immediately eligible)

5. Recreate auth (users, passkeys, and API keys are lost). With an empty
   auth store the server is back in the first-boot state: open the web UI
   and the setup page prompts you to create the admin account again.
   Alternatively, configure OIDC in `config.yaml` to skip local auth
   entirely.

   Note: `subflux reset-password --user <name>` resets the password of an
   EXISTING user (it answers 404 for an unknown username); it does not
   create accounts, so it cannot bootstrap a fresh store.

6. Re-apply any manual locks if needed (manual locks and sync offsets
   are not recoverable from the filesystem).

### What is lost

- Local auth users, passkeys, API keys
- Manual locks and manual download history
- Sync offsets (timing adjustments reset to zero)
- Adaptive backoff state (all providers become immediately eligible)

### What is rebuilt automatically

- Subtitle file inventory and coverage
- Auto download state (via `DetectExisting` during scan)
- Scan state and poll cursors (re-baselined on first poll/scan)

---

## 2. File-growth reclaim (bbolt compact)

### When to compact

bbolt reuses free pages internally but never shrinks the file. Monitor
the Prometheus metrics:

- `subflux_store_freelist_bytes` / `subflux_store_file_bytes` ratio
  exceeds ~50% (significant free space not returned to the OS)
- Or the file size exceeds 100 MB (unusually large for a subtitle metadata store)

Under normal operation, compaction is rarely needed. It becomes relevant
only after sustained high-churn workloads (mass reconcile + re-scan
cycles, bulk library reorganization).

### Compaction procedure

1. Stop the container:

   ```sh
   docker compose stop subflux
   ```

2. Run compaction:

   ```sh
   bbolt compact -o /config/subflux-compact.bolt /config/subflux.bolt
   ```

3. Verify the compacted file:

   ```sh
   bbolt check /config/subflux-compact.bolt
   ```

   Must report no errors.

4. Replace the original:

   ```sh
   mv /config/subflux-compact.bolt /config/subflux.bolt
   ```

5. Restart the container.

### Notes

- The `bbolt` CLI is NOT inside the container image (distroless, single
  binary). Run it on the host against the mounted config volume:
  `go install go.etcd.io/bbolt/cmd/bbolt@latest`, or use a throwaway
  container, e.g.

  ```sh
  docker run --rm -v /path/to/config:/config golang:alpine sh -c \
    'go install go.etcd.io/bbolt/cmd/bbolt@latest && \
     bbolt compact -o /config/subflux-compact.bolt /config/subflux.bolt'
  ```

- Compaction rewrites the file sequentially, reclaiming all free pages.
  The resulting file contains only live data.
- Do not run compaction while the container is running (bbolt holds an
  exclusive file lock).

---

## 3. Host kernel check (cutover checklist)

### Background

ext4 with the `fast_commit` feature (enabled by default on recent kernels)
had a bug that could corrupt mmap'd files (including bbolt) on unclean
shutdown. This affects the bbolt store because bbolt memory-maps its data
file for reads.

### Fixed versions

- Kernel 5.10.94+ (LTS)
- Kernel 5.15.17+ (LTS)

Upstream fixes were backported to both LTS branches.

### Check

```sh
uname -r
```

Must be >= 5.10.94 or >= 5.15.17 (within the respective branch).

### Common deployment platforms

| Platform                | Kernel | Status                   |
| ----------------------- | ------ | ------------------------ |
| TrueNAS Community (ZFS) | 6.x    | Safe (ZFS, not ext4)     |
| DietPi / Raspberry Pi   | 6.x    | Safe (above fix version) |
| Synology DSM            | 4.4.x  | Not affected             |

### ZFS hosts

Not affected. ZFS uses its own copy-on-write transaction model and is not
ext4. No action needed on TrueNAS or other ZFS-backed Docker hosts.

### If running on an affected kernel

Two options:

- Disable fast_commit on the ext4 volume:

  ```sh
  tune2fs -O ^fast_commit /dev/sdXn
  ```

  Requires unmounting the filesystem first.

- Upgrade the kernel to a fixed version before deploying the bbolt build.

### Verification

After deploying, confirm the store opens cleanly:

```sh
docker logs subflux 2>&1 | grep -i "store opened"
```

No `invalid database` or `checksum` errors in the first few lines of output.

---

## 4. Leftover SQLite-era files

Installs upgraded across the SQLite-to-bbolt cutover still hold the old
SQLite files. Nothing in the bbolt engine reads, prunes, or deletes them —
they are frozen dead weight (bounded at roughly the old database size plus
`backup.retention` snapshot copies of it). Up to four patterns exist:

| File | What it is |
| ---- | ---------- |
| `/config/subflux.db` | the old live SQLite database |
| `subflux.db-wal`, `subflux.db-shm` | WAL sidecars (unclean last shutdown) |
| `subflux-<timestamp>.db` | `VACUUM INTO` snapshots (if backups were on) |

Snapshots live in `backup.path` if you configured one, otherwise **next to
the database in `/config`** (the default). The backup pruner now only
manages `subflux-*.bolt`, so `.db` snapshots are never removed
automatically.

Once you are confident you will not roll back to a pre-bbolt version,
remove them manually:

```sh
# Default setup (snapshots next to the database):
rm /config/subflux.db /config/subflux.db-wal /config/subflux.db-shm /config/subflux-*.db

# If backup.path pointed somewhere else, also:
rm <backup.path>/subflux-*.db
```

`rm` will report "No such file" for patterns you never had (e.g. the WAL
sidecars after a clean shutdown, or snapshots if backups were never
enabled); that is expected and harmless.
