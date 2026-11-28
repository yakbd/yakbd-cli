#!/bin/bash
# macOS layout check driver: builds the checker, installs every case's bundle into ~/Library/Keyboard Layouts,
# runs the checker on it and writes OUT/summary.txt (one line per check). See README.md.
# usage: tools/macos-check/run.sh OUT [METHODS]
set -u
here=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: run.sh OUT [METHODS]}
methods=${2:-cgevent,sysevents}
mkdir -p "$out"
out=$(cd "$out" && pwd)
sum=$out/summary.txt
: >"$sum"

echo "INFO host : macOS $(sw_vers -productVersion) ($(sw_vers -buildVersion)) $(uname -m), $(swiftc --version 2>&1 | head -1)" >>"$sum"
if ! swiftc -swift-version 5 -O -o "$out/macos-check" "$here/check.swift" 2>"$out/build.txt"; then
	echo "FAIL build : swiftc failed, see build.txt" >>"$sum"
	cat "$out/build.txt"
	exit 1
fi

dest="$HOME/Library/Keyboard Layouts"
mkdir -p "$dest"
for c in "$here"/cases/*/; do
	name=$(basename "$c")
	bundle=$(ls -d "$c"*.bundle 2>/dev/null | head -1)
	if [ -z "$bundle" ] || [ ! -f "$c/expect.tsv" ]; then
		echo "FAIL $name case : needs one *.bundle and expect.tsv" >>"$sum"
		continue
	fi
	inst="$dest/$(basename "$bundle")"
	ditto "$bundle" "$inst"
	if [ -f "$c/quarantine" ]; then
		# What a browser or a zip tool leaves on a downloaded file.
		xattr -r -w com.apple.quarantine "0083;$(printf %x "$(date +%s)");Safari;$(uuidgen)" "$inst"
		echo "INFO $name quarantine : $(xattr -p com.apple.quarantine "$inst" 2>&1)" >>"$sum"
	fi
	echo "INFO $name gatekeeper : $(spctl --assess --type open --context context:primary-signature -v "$inst" 2>&1 | tr '\n' ' ')" >>"$sum"
	"$out/macos-check" "$name" "$inst" "$c/expect.tsv" "$methods" >>"$sum" 2>>"$out/stderr.txt"
	echo "INFO $name exit : $?" >>"$sum"
done

if grep -q '^FAIL' "$sum"; then
	log show --last 10m --style compact \
		--predicate 'eventMessage CONTAINS[c] "keylayout" OR eventMessage CONTAINS[c] "uchr" OR eventMessage CONTAINS[c] "Keyboard Layouts" OR process == "TextInputMenuAgent" OR process == "TextInputSwitcher"' \
		>"$out/log.txt" 2>&1 || true
fi
echo "pass $(grep -c '^PASS' "$sum"), fail $(grep -c '^FAIL' "$sum"), skip $(grep -c '^SKIP' "$sum")"
! grep -q '^FAIL' "$sum"
