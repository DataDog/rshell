# Journal fixtures

These fixtures contain four synthetic records with fixed machine, boot, and
timestamps. They contain no logs or identifiers copied from a real host. The
generation script also replaces the journal header's generator machine ID with
the fixed synthetic machine ID before verification.

The committed files were generated and verified with systemd 255.4 on Ubuntu
24.04. `regular-uncompressed.journal.gz` exercises the original regular layout.
`compact-keyed-zstd.journal.gz` exercises compact offsets, keyed hashes, and a
Zstandard-compressed DATA object. Gzip only keeps the sparse 8 MiB journal files
small in Git; tests decompress them before parsing.

On a Linux system with `systemd-journal-remote`, regenerate them with:

```sh
./generate.sh
```

Journal file and sequence IDs are randomized by systemd, so regeneration is
semantically deterministic but does not reproduce the same checksums. Review
the semantic test output and commit the updated `SHA256SUMS` with the fixtures.
