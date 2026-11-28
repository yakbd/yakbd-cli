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

# One checker pass: pass TAG BUNDLE EXPECT WAIT. Its lines go to OUT/pass.txt first.
pass() {
	MACOS_CHECK_WAIT=$4 "$out/macos-check" "$1" "$2" "$3" "$methods" >"$out/pass.txt" 2>>"$out/stderr.txt"
	echo "INFO $1 exit : $?" >>"$out/pass.txt"
	hitoolbox "after $1"
	grep -q "^PASS $1 current " "$out/pass.txt"
}
# Keeps a pass's lines; a pass that a later pass retries keeps its failures as RETRY, not FAIL.
keep() {
	if [ "$1" = retried ]; then sed 's/^FAIL /RETRY /' "$out/pass.txt" >>"$sum"; else cat "$out/pass.txt" >>"$sum"; fi
}

hitoolbox "before"
# The control: one of Apple's own layouts, installed but not enabled, through the same steps.
pass control -control - 5
keep final

for c in "$here"/cases/*/; do
	name=$(basename "$c")
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
	# Until the layout is selected: the plain pass; the same again in a fresh process; after adding it to the
	# enabled input sources in the HIToolbox preferences; after restarting the text-input agents (the nearest
	# thing to signing out and in that a runner allows).
	if pass "$name" "$inst" "$c/expect.tsv" 60; then keep final; else
		keep retried
		if pass "$name/again" "$inst" "$c/expect.tsv" 15; then keep final; else
			keep retried
			kname=$(basename "$bundle" .bundle)
			kid=$(sed -n 's/.*<keyboard [^>]* id="\(-\{0,1\}[0-9]*\)".*/\1/p' "$inst/Contents/Resources/$kname.keylayout" | head -1)
			defaults write com.apple.HIToolbox AppleEnabledInputSources -array-add \
				"<dict><key>InputSourceKind</key><string>Keyboard Layout</string><key>KeyboardLayout ID</key><integer>$kid</integer><key>KeyboardLayout Name</key><string>$kname</string></dict>"
			echo "INFO $name preferences : added KeyboardLayout ID $kid, Name $kname (exit $?)" >>"$sum"
			if pass "$name/prefs" "$inst" "$c/expect.tsv" 15; then keep final; else
				keep retried
				killall cfprefsd TextInputMenuAgent TextInputSwitcher >/dev/null 2>&1
				echo "INFO $name restart : killall cfprefsd TextInputMenuAgent TextInputSwitcher (exit $?)" >>"$sum"
				sleep 5
				pass "$name/restart" "$inst" "$c/expect.tsv" 30
				keep final
			fi
		fi
	fi
	echo "INFO $name gatekeeper : $(spctl --assess --type open --context context:primary-signature -v "$inst" 2>&1 | tr '\n' ' ')" >>"$sum"
done

if grep -q '^FAIL' "$sum"; then
	log show --last 10m --style compact \
		--predicate 'eventMessage CONTAINS[c] "keylayout" OR eventMessage CONTAINS[c] "uchr" OR eventMessage CONTAINS[c] "Keyboard Layouts" OR process == "TextInputMenuAgent" OR process == "TextInputSwitcher"' \
		>"$out/log.txt" 2>&1 || true
fi
echo "pass $(grep -c '^PASS' "$sum"), fail $(grep -c '^FAIL' "$sum"), skip $(grep -c '^SKIP' "$sum"), retried $(grep -c '^RETRY' "$sum")"
! grep -q '^FAIL' "$sum"
