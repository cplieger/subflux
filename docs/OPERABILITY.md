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

4. Wait for one full scan cycle (runs after the 30s startup delay). Library state is rebuilt automatically from the filesystem:
   - `subtitle_state` (auto rows) - detected from on-disk subtitle files
   - `subtitle_files` - inventoried from media directories
   - `scan_state` - populated as each item is scanned
   - `search_attempts` - starts empty (all items immediately eligible)

5. Recreate auth (users, passkeys, and API keys are lost):

   ```sh
   docker exec subflux subflux reset-password --user admin
   ```

   This creates the admin user via the first-boot flow if none exist. Alternatively, configure OIDC in `config.yaml` to skip local auth entirely.

6. Re-apply any manual locks if needed (manual locks and sync offsets are not recoverable from the filesystem).

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

bbolt reuses free pages internally but never shrinks the file. Monitor the Prometheus metrics:

- `subflux_store_freelist_bytes` / `subflux_store_file_bytes` ratio exceeds ~50% (significant free space not returned to the OS)
- Or the file size exceeds 100 MB (unusually large for a subtitle metadata store)

Under normal operation, compaction is rarely needed. It becomes relevant only after sustained high-churn workloads (mass reconcile + re-scan cycles, bulk library reorganization).

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

- The `bbolt` CLI is available in the container image. On the host: `go install go.etcd.io/bbolt/cmd/bbolt@latest`.
- Compaction rewrites the file sequentially, reclaiming all free pages. The resulting file contains only live data.
- Do not run compaction while the container is running (bbolt holds an exclusive file lock).

---

## 3. Host kernel check (cutover checklist)

### Background

ext4 with the `fast_commit` feature (enabled by default on recent kernels) had a bug that could corrupt mmap'd files (including bbolt) on unclean shutdown. This affects the bbolt store because bbolt memory-maps its data file for reads.

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

Not affected. ZFS uses its own copy-on-write transaction model and is not ext4. No action needed on TrueNAS or other ZFS-backed Docker hosts.

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
