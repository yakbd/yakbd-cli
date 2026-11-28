#!/bin/bash
# macOS layout check driver: builds the checker, installs every case's bundle into ~/Library/Keyboard Layouts,
# runs the checker on it and writes OUT/summary.txt (one line per check). See README.md.
# usage: tools/macos-check/run.sh OUT [METHODS]
# MACOS_CHECK_PLACE=system installs into /Library/Keyboard Layouts (with sudo) instead.
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
as=
if [ "${MACOS_CHECK_PLACE:-user}" = system ]; then
	dest="/Library/Keyboard Layouts"
	as=sudo
fi
echo "INFO host install-folder : $dest" >>"$sum"
$as mkdir -p "$dest"
hitoolbox() {
	{ echo "== $1"; defaults read com.apple.HIToolbox 2>&1; } >>"$out/hitoolbox.txt"
}

# One checker pass: pass TAG BUNDLE EXPECT WAIT; its lines go to the summary.
pass() {
	MACOS_CHECK_WAIT=$4 "$out/macos-check" "$1" "$2" "$3" "$methods" >>"$sum" 2>>"$out/stderr.txt"
	echo "INFO $1 exit : $?" >>"$sum"
	hitoolbox "after $1"
}

hitoolbox "before"
# The control: one of Apple's own layouts, installed but not enabled, through the same steps.
pass control -control - 5

# Cases with a layout.kbl.json and no bundle are generated here by yakbd itself: the bundle by
# `yakbd compile -target macos` and expect.tsv from the .kbl.json (the model), so the run checks the tool's build.
yakbd=
for c in "$here"/cases/*/; do
	name=$(basename "$c")
	if [ -f "$c/layout.kbl.json" ] && ! ls -d "$c"*.bundle >/dev/null 2>&1; then
		if [ -z "$yakbd" ]; then
			yakbd=$out/yakbd
			if ! (cd "$here/../.." && go build -o "$yakbd" ./cmd/yakbd) 2>"$out/build-yakbd.txt"; then
				echo "FAIL build yakbd : go build failed, see build-yakbd.txt" >>"$sum"
				yakbd=none
			fi
		fi
		gen=$out/cases/$name
		mkdir -p "$gen"
		[ -f "$c/quarantine" ] && cp "$c/quarantine" "$gen/"
		if [ "$yakbd" = none ] || ! "$yakbd" compile -target macos -q -emit-expectations -max-expectations 60 -o "$gen" "$c/layout.kbl.json" >"$gen/compile.txt" 2>&1; then
			echo "FAIL $name generate : yakbd compile -target macos failed: $(tr '\n' ' ' <"$gen/compile.txt" | cut -c1-300)" >>"$sum"
			continue
		fi
		echo "INFO $name generate : $(grep -c . "$gen/expect.tsv") expectation lines from layout.kbl.json" >>"$sum"
		c=$gen/
	fi
	bundle=$(ls -d "$c"*.bundle 2>/dev/null | head -1)
	if [ -z "$bundle" ] || [ ! -f "$c/expect.tsv" ]; then
		echo "FAIL $name case : needs one *.bundle and expect.tsv" >>"$sum"
		continue
	fi
	inst="$dest/$(basename "$bundle")"
	$as ditto "$bundle" "$inst"
	if [ -f "$c/quarantine" ]; then
		# What a browser or a zip tool leaves on a downloaded file.
		$as xattr -r -w com.apple.quarantine "0083;$(printf %x "$(date +%s)");Safari;$(uuidgen)" "$inst"
		echo "INFO $name quarantine : $(xattr -p com.apple.quarantine "$inst" 2>&1)" >>"$sum"
	fi
	# Register first (a checker run that stops after registration), then enable the layout the way the Text Input
	# settings do: an entry in AppleEnabledInputSources of the HIToolbox preferences (keyboard id and name from the
	# .keylayout). The checker that follows is a new process and finds the layout enabled. TISEnableInputSource
	# alone did not enable a bundle in ~/Library/Keyboard Layouts, and an entry written before the layout was
	# registered was not always honoured (README.md, runs).
	MACOS_CHECK_REGISTER_ONLY=1 "$out/macos-check" "$name/register" "$inst" "$c/expect.tsv" none >>"$sum" 2>>"$out/stderr.txt"
	echo "INFO $name/register exit : $?" >>"$sum"
	kname=$(basename "$bundle" .bundle)
	kid=$(sed -n 's/.*<keyboard [^>]* id="\(-\{0,1\}[0-9]*\)".*/\1/p' "$inst/Contents/Resources/$kname.keylayout" | head -1)
	if [ -z "$kid" ] || [ ! -f "$inst/Contents/Resources/$kname.keylayout" ]; then
		echo "FAIL $name case : no Contents/Resources/$kname.keylayout with a <keyboard ... id=\"N\"> line" >>"$sum"
		continue
	fi
	defaults write com.apple.HIToolbox AppleEnabledInputSources -array-add \
		"<dict><key>InputSourceKind</key><string>Keyboard Layout</string><key>KeyboardLayout ID</key><integer>$kid</integer><key>KeyboardLayout Name</key><string>$kname</string></dict>"
	echo "INFO $name preferences : enabled-sources entry KeyboardLayout ID $kid, Name $kname (exit $?)" >>"$sum"
	pass "$name" "$inst" "$c/expect.tsv" 60
	echo "INFO $name gatekeeper : $(spctl --assess --type open --context context:primary-signature -v "$inst" 2>&1 | tr '\n' ' ')" >>"$sum"
done

if grep -q '^FAIL' "$sum"; then
	log show --last 10m --style compact \
		--predicate 'eventMessage CONTAINS[c] "keylayout" OR eventMessage CONTAINS[c] "uchr" OR eventMessage CONTAINS[c] "Keyboard Layouts" OR process == "TextInputMenuAgent" OR process == "TextInputSwitcher"' \
		>"$out/log.txt" 2>&1 || true
fi
echo "pass $(grep -c '^PASS' "$sum"), fail $(grep -c '^FAIL' "$sum"), skip $(grep -c '^SKIP' "$sum")"
! grep -q '^FAIL' "$sum"
