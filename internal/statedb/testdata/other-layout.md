# other-layout.bolt

A bbolt file in another bolttable layout, read by `TestOtherLayoutNumberResets`.

- `bolttable`: key `layout` holds the big-endian `uint32` 2, a layout number bolttable `v1.0.0-dev.3` (layout 1) does not write.
- `core`: one table bucket named by the byte `0x03` (statedb's cursors prefix), holding the row `src-a` = `{"cursor":"AQID"}`. The bucket stores no digest.

Made with `go.etcd.io/bbolt` v1.5.0 and default options by the same throwaway program as `today-640a639.bolt`, outside this repository.
