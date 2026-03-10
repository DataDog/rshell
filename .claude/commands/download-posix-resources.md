---
description: Download reference test suites and GTFOBins data for the implement-posix-command skill
---

Download the reference resources used by the `implement-posix-command` skill and store them in the `resources/` directory. These resources are committed to git so they are available offline.

## What to download

### 1. GNU coreutils tests

Download the GNU coreutils test directory:

```bash
rm -rf /tmp/coreutils-master
curl -sL https://github.com/coreutils/coreutils/archive/refs/heads/master.tar.gz | tar -xz -C /tmp
rm -rf resources/gnu-coreutils-tests
mkdir -p resources/gnu-coreutils-tests
cp -R /tmp/coreutils-master/tests/* resources/gnu-coreutils-tests/
rm -rf /tmp/coreutils-master
```

### 2. uutils/coreutils tests

Download the uutils (Rust rewrite) test files:

```bash
rm -rf /tmp/coreutils-main
curl -sL https://github.com/uutils/coreutils/archive/refs/heads/main.tar.gz | tar -xz -C /tmp
rm -rf resources/uutils-tests
mkdir -p resources/uutils-tests
cp -R /tmp/coreutils-main/tests/by-util/* resources/uutils-tests/
rm -rf /tmp/coreutils-main
```

### 3. GTFOBins pages

Download the GTFOBins markdown pages:

```bash
rm -rf /tmp/GTFOBins.github.io-master
curl -sL https://github.com/GTFOBins/GTFOBins.github.io/archive/refs/heads/master.tar.gz | tar -xz -C /tmp
rm -rf resources/gtfobins
mkdir -p resources/gtfobins
cp /tmp/GTFOBins.github.io-master/_gtfobins/* resources/gtfobins/
rm -rf /tmp/GTFOBins.github.io-master
```

## Instructions

Run each of the three download sections above in sequence. After all downloads complete, verify the resources exist:

```bash
echo "GNU coreutils tests:" && ls resources/gnu-coreutils-tests/ | head -10
echo "uutils tests:" && ls resources/uutils-tests/ | head -10
echo "GTFOBins pages:" && ls resources/gtfobins/ | head -10
```

Report the total size and file count:

```bash
echo "Total resource sizes:"
du -sh resources/gnu-coreutils-tests resources/uutils-tests resources/gtfobins
echo "File counts:"
find resources/gnu-coreutils-tests -type f | wc -l
find resources/uutils-tests -type f | wc -l
find resources/gtfobins -type f | wc -l
```
