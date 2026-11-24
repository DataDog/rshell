#!/usr/bin/env bash
# Print a temporary PATH directory containing one verified GNU reference.
# Only wc needs the pre-9.5 word-counting contract used by rshell.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 {cat|head|tail|wc}" >&2
  exit 2
fi
utility=$1
case "$utility" in
  cat|head|tail|wc) ;;
  *) echo "unsupported GNU reference: $utility" >&2; exit 2 ;;
esac

reference=''
for candidate in "gnu$utility" "g$utility" "$utility"; do
  if ! executable=$(command -v "$candidate"); then
    continue
  fi
  if ! version=$(LC_ALL=C "$executable" --version 2>/dev/null); then
    continue
  fi
  version=${version%%$'\n'*}
  case "$version" in
    "$utility (GNU coreutils) "*) ;;
    *) continue ;;
  esac
  if [[ "$utility" == wc && "$version" != 'wc (GNU coreutils) 9.4' ]]; then
    continue
  fi
  reference=$executable
  break
done

work_dir=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/rshell-gnu-$utility.XXXXXX")
keep_reference=0
trap 'if [[ "$keep_reference" -eq 0 ]]; then rm -rf -- "$work_dir"; fi' EXIT
mkdir "$work_dir/bin"
if [[ -z "$reference" ]]; then
  # GNU's release announcement publishes this SHA256 checksum:
  # https://lists.gnu.org/archive/html/info-gnu/2023-08/msg00007.html
  checksum=ea613a4cf44612326e917201bbbcdfbd301de21ffc3b59b6e5c07e040b275e52
  archive="$work_dir/coreutils.tar.xz"
  curl --fail --location --retry 2 --connect-timeout 15 --max-time 120 --output "$archive" \
    https://ftp.gnu.org/gnu/coreutils/coreutils-9.4.tar.xz >&2
  printf '%s  %s\n' "$checksum" "$archive" | sha256sum --check >&2
  tar -xJf "$archive" -C "$work_dir"
  (
    cd "$work_dir/coreutils-9.4"
    # GNU 9.4 predates GCC's default switch to C23.
    FORCE_UNSAFE_CONFIGURE=1 ./configure --disable-nls --disable-dependency-tracking \
      CFLAGS='-O2 -std=gnu17' >&2
    make -j2 >&2
  )
  reference="$work_dir/coreutils-9.4/src/$utility"
fi

# PATH entries can be relative; the symlink must outlive this working directory.
reference=$(realpath -- "$reference")
ln -s "$reference" "$work_dir/bin/$utility"
LC_ALL=C "$work_dir/bin/$utility" --version >&2
if [[ "$utility" == wc ]]; then
  # Fail before fuzzing if the locale or executable has incompatible semantics.
  control=$(printf '\001' | LC_ALL=C.UTF-8 "$reference" -w)
  invalid=$(printf '\377' | LC_ALL=C.UTF-8 "$reference" -w)
  joiner=$(printf '0\342\201\2400' | LC_ALL=C.UTF-8 "$reference" -w)
  if [[ "$control" -ne 0 || "$invalid" -ne 0 || "$joiner" -ne 2 ]]; then
    echo 'GNU wc 9.4 with C.UTF-8 and default GNU word-counting semantics is required' >&2
    exit 1
  fi
fi
keep_reference=1
printf '%s\n' "$work_dir/bin"
