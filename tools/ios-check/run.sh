#!/bin/bash
# iOS keyboard check (README.md): builds the check app with its keyboard extension unsigned, then for every iOS
# simulator runtime this Xcode can use: a fresh simulator, simctl install, the hosted unit test (weaker level),
# enabling the keyboard through the AppleKeyboards default, a reboot and the UI test that types with the extension.
# Writes OUTDIR/summary.txt, one line per check: PASS|FAIL|INFO <check> [: detail].
#
# Environment: IOS_CHECK_XCODE    Xcode version or prefix ("16.4", "26") or "latest" (default)
#              IOS_CHECK_SIGNING  "none" (CODE_SIGNING_ALLOWED=NO, default) or "adhoc" (CODE_SIGN_IDENTITY=-)
#              IOS_CHECK_RUNTIMES "all" (default) or iOS versions separated by spaces ("18.5 26.2")
set -uo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
out="${1:?usage: run.sh OUTDIR}"
mkdir -p "$out"
out="$(cd "$out" && pwd)"
summary="$out/summary.txt"
: >"$summary"
failed=0

line() {
	echo "$*" | tee -a "$summary"
	case "$1" in FAIL) failed=1 ;; esac
}
now() { date +%s; }
tab=$'\t'

# Version "a.b[.c]" as a number a*10000+b*100+c, for comparisons.
vnum() {
	local a b c
	IFS=. read -r a b c <<<"$1"
	echo $(( ${a:-0} * 10000 + ${b:-0} * 100 + ${c:-0} ))
}

line INFO runner : "macOS $(sw_vers -productVersion) $(uname -m)"
for x in /Applications/Xcode*.app; do
	line INFO xcode-installed : "$(basename "$x")"
done

want="${IOS_CHECK_XCODE:-latest}"
pick=""
for x in $(ls -d /Applications/Xcode_*.app 2>/dev/null | grep -viE 'beta|release_candidate|_rc' | sort -V); do
	v="${x#/Applications/Xcode_}"
	v="${v%.app}"
	case "$v" in
	"$want" | "$want".*) pick="$x" ;;
	esac
	[ "$want" = latest ] && pick="$x"
done
if [ -z "$pick" ]; then
	line FAIL xcode-select : "no /Applications/Xcode_$want*.app"
	exit 1
fi
export DEVELOPER_DIR="$pick/Contents/Developer"
sdk="$(xcrun --sdk iphonesimulator --show-sdk-version)"
line INFO xcode-selected : "$(basename "$pick"), $(xcodebuild -version | tr '\n' ' '), simulator SDK $sdk"
command -v xcodegen >/dev/null || { line FAIL xcodegen : "xcodegen not installed"; exit 1; }
line INFO xcodegen : "$(xcodegen --version 2>&1 | tr '\n' ' ')"

# iOS runtimes this Xcode can run: available and not newer than its simulator SDK.
runtimes="$out/runtimes.txt"
xcrun simctl list runtimes -j >"$out/runtimes.json"
jq -r '.runtimes[] | select((.platform // "") == "iOS" or (.identifier | contains("iOS"))) |
	"\(.version)\t\(.identifier)\t\(.isAvailable)"' "$out/runtimes.json" | sort -V >"$out/runtimes-all.txt"
: >"$runtimes"
while IFS=$'\t' read -r v id avail; do
	line INFO runtime-listed : "iOS $v $id available=$avail"
	[ "$avail" = true ] || continue
	[ "$(vnum "$v")" -le "$(vnum "$sdk")" ] || continue
	if [ "${IOS_CHECK_RUNTIMES:-all}" != all ]; then
		case " $IOS_CHECK_RUNTIMES " in *" $v "*) ;; *) continue ;; esac
	fi
	printf '%s\t%s\n' "$v" "$id" >>"$runtimes"
done <"$out/runtimes-all.txt"
[ -s "$runtimes" ] || { line FAIL runtimes : "no usable iOS runtime for SDK $sdk"; exit 1; }

case "${IOS_CHECK_SIGNING:-none}" in
none) signing=(CODE_SIGNING_ALLOWED=NO) ;;
adhoc) signing=(CODE_SIGN_IDENTITY=- CODE_SIGN_STYLE=Manual CODE_SIGNING_REQUIRED=NO DEVELOPMENT_TEAM=) ;;
*) line FAIL signing : "unknown IOS_CHECK_SIGNING ${IOS_CHECK_SIGNING}"; exit 1 ;;
esac
line INFO signing : "${signing[*]}"

# The Simulator app's setting that otherwise connects the Mac's keyboard, which hides software keyboards.
defaults write com.apple.iphonesimulator ConnectHardwareKeyboard -bool false

# Collects the marker lines of a test log into the summary, with a prefix.
collect() {
	local log="$1" prefix="$2" n=0 status rest
	while read -r _ status rest; do
		line "$status" "$prefix/$rest"
		n=$((n + 1))
	done < <(grep '^YAKCASE ' "$log")
	[ "$n" -gt 0 ] || line FAIL "$prefix/results" : "no result lines in $(basename "$log")"
}

errors() {
	grep -E 'error:|\*\* .* FAILED \*\*|Testing failed|Test Case .* failed' "$1" | head -8 | while read -r l; do
		line INFO "$2" : "$l"
	done
}

simlog() {
	xcrun simctl spawn "$1" log show --style compact --last 10m \
		--predicate 'process CONTAINS "YakCheck" OR eventMessage CONTAINS "org.yakbd.check" OR subsystem CONTAINS "keyboard"' \
		>"$2" 2>&1 || true
}

reboot() {
	xcrun simctl shutdown "$1" >/dev/null 2>&1 || true
	xcrun simctl boot "$1" && xcrun simctl bootstatus "$1" -b >/dev/null
}

for casedir in "$here"/cases/*/; do
	case="$(basename "$casedir")"
	proj="$out/build/$case"
	mkdir -p "$proj/Case"
	cp -R "$here/App" "$here/Keyboard" "$here/UITests" "$here/UnitTests" "$here/Shared" "$here/Plists" \
		"$here/project.yml" "$proj/"
	mkdir -p "$out/$case"
	cp "$casedir/layout.json" "$casedir/expect.tsv" "$proj/Case/"
	if (cd "$proj" && xcodegen generate --spec project.yml) >"$out/$case-xcodegen.txt" 2>&1; then
		line PASS "$case/xcodegen"
		cp "$proj/YakCheck.xcodeproj/project.pbxproj" "$out/$case-project.pbxproj"
	else
		line FAIL "$case/xcodegen" : "$(tail -3 "$out/$case-xcodegen.txt" | tr '\n' ' ')"
		continue
	fi

	t0="$(now)"
	if xcodebuild build-for-testing -project "$proj/YakCheck.xcodeproj" -scheme YakCheck \
		-destination 'generic/platform=iOS Simulator' -derivedDataPath "$proj/dd" ARCHS=arm64 ONLY_ACTIVE_ARCH=NO \
		"${signing[@]}" >"$out/$case-build.txt" 2>&1; then
		line PASS "$case/build" : "$(( $(now) - t0 )) s"
	else
		line FAIL "$case/build" : "$(( $(now) - t0 )) s, log $case-build.txt"
		errors "$out/$case-build.txt" "$case/build"
		continue
	fi
	app="$proj/dd/Build/Products/Debug-iphonesimulator/YakCheck.app"
	appex="$app/PlugIns/YakCheckKeyboard.appex"
	if [ -f "$appex/Info.plist" ]; then
		line PASS "$case/embedded" : "$(plutil -extract NSExtension.NSExtensionPointIdentifier raw "$appex/Info.plist")"
	else
		line FAIL "$case/embedded" : "no PlugIns/YakCheckKeyboard.appex in the app"
	fi
	line INFO "$case/signature" : "$(codesign -dv "$app" 2>&1 | grep -E 'Signature|flags' | tr '\n' ' ')"

	while IFS=$'\t' read -r v rid; do
		label="$case/iOS-$v"
		tr0="$(now)"
		dt="$(jq -r --arg id "$rid" '.runtimes[] | select(.identifier == $id) | .supportedDeviceTypes[] |
			select(.productFamily == "iPhone") | "\(.name)\t\(.identifier)"' "$out/runtimes.json" |
			grep -E "^iPhone [0-9]+$tab" | tail -1)"
		[ -n "$dt" ] || dt="$(jq -r --arg id "$rid" '.runtimes[] | select(.identifier == $id) |
			.supportedDeviceTypes[] | select(.productFamily == "iPhone") | "\(.name)\t\(.identifier)"' \
			"$out/runtimes.json" | tail -1)"
		if [ -z "$dt" ]; then
			line FAIL "$label/device" : "no iPhone device type"
			continue
		fi
		udid="$(xcrun simctl create "yakbd-check-$v" "${dt#*$tab}" "$rid" 2>"$out/$label-create.txt" | tr -d '\n')"
		mkdir -p "$(dirname "$out/$label")"
		if [ -z "$udid" ]; then
			line FAIL "$label/device" : "simctl create failed: $(head -2 "$out/$label-create.txt" 2>/dev/null)"
			continue
		fi
		line INFO "$label/device" : "${dt%%$tab*} $udid"
		if ! reboot "$udid"; then
			line FAIL "$label/boot"
			xcrun simctl delete "$udid" >/dev/null 2>&1
			continue
		fi

		if xcrun simctl install "$udid" "$app" >"$out/$label-install.txt" 2>&1 &&
			xcrun simctl get_app_container "$udid" org.yakbd.check >/dev/null 2>&1; then
			line PASS "$label/install"
		else
			line FAIL "$label/install" : "$(head -3 "$out/$label-install.txt" | tr '\n' ' ')"
		fi
		line INFO "$label/pluginkit" : "$(xcrun simctl spawn "$udid" pluginkit -m -i org.yakbd.check.keyboard 2>&1 | head -2 | tr '\n' ' ')"

		# Weaker level first, before the keyboard is enabled; then a reboot, so the UI test gets a fresh boot.
		t0="$(now)"
		xcodebuild test-without-building -project "$proj/YakCheck.xcodeproj" -scheme YakCheck \
			-destination "platform=iOS Simulator,id=$udid" -derivedDataPath "$proj/dd" \
			-only-testing:YakCheckUnitTests -resultBundlePath "$out/$label-unit.xcresult" \
			>"$out/$label-unit.txt" 2>&1
		st=$?
		collect "$out/$label-unit.txt" "$label"
		[ "$st" -eq 0 ] && line PASS "$label/unit-run" : "$(( $(now) - t0 )) s" ||
			{ line FAIL "$label/unit-run" : "exit $st, $(( $(now) - t0 )) s"; errors "$out/$label-unit.txt" "$label/unit-run"; }

		xcrun simctl spawn "$udid" defaults write -g AppleKeyboards -array \
			"en_US@sw=QWERTY;hw=Automatic" "org.yakbd.check.keyboard"
		xcrun simctl spawn "$udid" defaults write -g AppleKeyboardsExpanded -bool true
		reboot "$udid" || line FAIL "$label/reboot"
		kb="$(xcrun simctl spawn "$udid" defaults read -g AppleKeyboards 2>&1 | tr -s ' \n' ' ')"
		case "$kb" in
		*org.yakbd.check.keyboard*) line PASS "$label/enable-default" : "$kb" ;;
		*) line FAIL "$label/enable-default" : "$kb" ;;
		esac

		t0="$(now)"
		xcodebuild test-without-building -project "$proj/YakCheck.xcodeproj" -scheme YakCheck \
			-destination "platform=iOS Simulator,id=$udid" -derivedDataPath "$proj/dd" \
			-only-testing:YakCheckUITests -resultBundlePath "$out/$label-ui.xcresult" \
			>"$out/$label-ui.txt" 2>&1
		st=$?
		collect "$out/$label-ui.txt" "$label"
		[ "$st" -eq 0 ] && line PASS "$label/ui-run" : "$(( $(now) - t0 )) s" ||
			{ line FAIL "$label/ui-run" : "exit $st, $(( $(now) - t0 )) s"; errors "$out/$label-ui.txt" "$label/ui-run"; }
		simlog "$udid" "$out/$label-simlog.txt"

		xcrun simctl shutdown "$udid" >/dev/null 2>&1
		xcrun simctl delete "$udid" >/dev/null 2>&1
		line INFO "$label/time" : "$(( $(now) - tr0 )) s for this runtime"
	done <"$runtimes"
done
exit "$failed"
