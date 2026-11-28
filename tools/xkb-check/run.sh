#!/bin/bash
# xkb-check driver: builds the test images once, then types every case of every layout under
# every stack and writes OUTDIR/summary.txt (one line per case) plus OUTDIR/logs/.
#
# usage: tools/xkb-check/run.sh OUTDIR
#
# Environment:
#   XKB_CHECK_PREFIX   prefix of every image and container name (default "xkb-check-")
#   XKB_CHECK_STACKS   stacks to run (default "distro x11 weston sway kwin mutter"; also "gnome-shell")
#   XKB_CHECK_DISTROS  base images for the distro stack
#                      (default "debian:stable ubuntu:latest fedora:latest archlinux:latest")
#   XKB_CHECK_NO_BUILD set to skip the image builds (reuse the images of an earlier run)
#
# Needs only Docker (rootless is fine); no root, no /dev/uinput, no GPU. MIT-0.
set -u
here=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: run.sh OUTDIR}
prefix=${XKB_CHECK_PREFIX:-xkb-check-}
stacks=${XKB_CHECK_STACKS:-distro x11 weston sway kwin mutter}
distros=${XKB_CHECK_DISTROS:-debian:stable ubuntu:latest fedora:latest archlinux:latest}
mkdir -p "$out/logs"
chmod 777 "$out/logs"   # the container user writes here (rootless Docker maps it to another uid)
summary=$out/summary.txt
: > "$summary"

img_name() { echo "${prefix}$(echo "$1" | tr ':/' '--')"; }

build() { # build TAG DOCKERFILE [BUILD-ARG]
  local tag=$1 file=$2 arg=${3:-}
  [ -n "${XKB_CHECK_NO_BUILD:-}" ] && return 0
  if docker build -t "$tag" ${arg:+--build-arg "$arg"} -f "$here/images/$file" "$here" \
       > "$out/logs/build-$tag.log" 2>&1; then
    return 0
  fi
  echo "build $tag FAIL see logs/build-$tag.log" >> "$summary"
  return 1
}

run_in() { # run_in IMAGE NAME ARGS...: inside.sh in a fresh container; prints its result lines
  local img=$1 name=$2; shift 2
  docker run --rm --name "${prefix}$name" -u tester -e KEEP_LOGS=/logs/$name \
    -v "$out/logs:/logs" -v "$here:/xkb-check:ro" "$img" \
    bash /xkb-check/inside.sh "$@" 2> "$out/logs/$name.stderr" |
    grep -E '^(version|distro|x11|weston|sway|kwin|mutter|gnome-shell) '
}

desktop=${prefix}desktop
for s in $stacks; do
  case $s in
    distro)
      for d in $distros; do
        img=$(img_name "$d")
        build "$img" distro.Dockerfile "BASE=$d" || continue
        run_in "$img" "versions-$(echo "$d" | tr ':/' '--')" versions >> "$summary"
        n=$(run_in "$img" "distro-$(echo "$d" | tr ':/' '--')" distro |
          sed "s/^distro /distro($d) /" | tee -a "$summary" | wc -l)
        [ "$n" -gt 0 ] || echo "distro($d) - - - FAIL no result lines" >> "$summary"
      done ;;
    *)
      if [ -z "${desktop_built:-}" ]; then
        build "$desktop" desktop.Dockerfile || break
        desktop_built=1
        run_in "$desktop" versions-desktop versions | sed 's/^version /version desktop-image /' >> "$summary"
      fi
      n=$(run_in "$desktop" "stack-$s" "$s" gtk qt | tee -a "$summary" | wc -l)
      [ "$n" -gt 0 ] || echo "$s - - - FAIL no result lines (see logs/stack-$s.stderr)" >> "$summary" ;;
  esac
done

# Compare with baseline.txt (the results of the run recorded in README.md, reasons cut off):
# a line that differs is a change in behaviour, in either direction.
results() { grep -E ' (PASS|FAIL)' "$1" | grep -v '^total ' | cut -d' ' -f1-5 | LC_ALL=C sort; }
results "$summary" > "$out/results.txt"
pass=$(grep -c ' PASS$' "$out/results.txt" 2>/dev/null); fail=$(grep -c ' FAIL$' "$out/results.txt" 2>/dev/null)
changed=0
if [ -f "$here/baseline.txt" ]; then
  diff <(LC_ALL=C sort "$here/baseline.txt") "$out/results.txt" | grep -E '^[<>]' |
    sed 's/^</changed was:/; s/^>/changed now:/' > "$out/changed.txt"
  changed=$(grep -c . "$out/changed.txt")
  cat "$out/changed.txt" >> "$summary"
fi
echo "total PASS=$pass FAIL=$fail CHANGED=$changed (against baseline.txt)" >> "$summary"
cat "$summary"
