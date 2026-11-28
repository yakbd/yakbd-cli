#!/usr/bin/env bash
# The apk package's CI control (docs/apk.md §4), on a Linux runner with the Android SDK (never on users'
# machines). It fails, never skips, when a tool is missing.
#  1. APKs written from scratch by tools/apkcheck (RSA test key, EC key): apksigner verifies v1, v2 and v3;
#     zipalign -c -p 4 passes; aapt2 dumps the manifest, the keyboard-layouts XML and the resource table.
#  2. An APK linked by aapt2 from aapt2/ (UTF-8 string pools, aapt2's attribute order) repackaged by
#     tools/apkcheck -rewrite: new package, label and asset; it verifies and aapt2 reads the renamed names.
set -euo pipefail
OUT=${1:?usage: control.sh OUTDIR}
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
: "${ANDROID_HOME:?ANDROID_HOME is not set}"
BT=$(ls "$ANDROID_HOME/build-tools" | sort -V | tail -n 1)
TOOLS="$ANDROID_HOME/build-tools/$BT"
for t in aapt2 apksigner zipalign; do
  [ -x "$TOOLS/$t" ] || { echo "FAIL: $TOOLS/$t missing"; exit 1; }
done
PLATFORM=android-35
JAR="$ANDROID_HOME/platforms/$PLATFORM/android.jar"
if [ ! -f "$JAR" ]; then
  yes | "$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager" "platforms;$PLATFORM" > "$OUT/sdkmanager.txt"
fi
[ -f "$JAR" ] || { echo "FAIL: $JAR missing"; exit 1; }
echo "build-tools $BT, $PLATFORM"
fail=0
check() { # name, command...: prints PASS/FAIL
  local name=$1; shift
  if "$@" > "$OUT/$name.txt" 2>&1; then echo "PASS $name"; else echo "FAIL $name (see $name.txt)"; cat "$OUT/$name.txt"; fail=1; fi
}
verified() { # apk: apksigner must report v1, v2 and v3
  "$TOOLS/apksigner" verify --verbose --min-sdk-version 21 "$1" | tee /dev/stderr |
    grep -c -E '^Verified using v(1|2|3) scheme .*: true$' | grep -qx 3
}
has() { # pattern, command...: the command's output contains the pattern
  local p=$1; shift
  "$@" | tee /dev/stderr | grep -q -- "$p"
}

(cd "$ROOT" && go run ./tools/apkcheck -o "$OUT")
for a in kcm-test kcm-test-ec; do
  apk="$OUT/$a.apk"
  check "$a-verify" verified "$apk"
  check "$a-zipalign" "$TOOLS/zipalign" -c -p -v 4 "$apk"
  check "$a-badging" has "package: name='org.yakbd.test.kcm" "$TOOLS/aapt2" dump badging "$apk"
  check "$a-manifest" has "android.hardware.input.metadata.KEYBOARD_LAYOUTS" "$TOOLS/aapt2" dump xmltree --file AndroidManifest.xml "$apk"
  check "$a-layouts" has "keyboardLayout(0x010103ab)=@0x7f020000" "$TOOLS/aapt2" dump xmltree --file res/xml/keyboard_layouts.xml "$apk"
  check "$a-resources" has "raw/test" "$TOOLS/aapt2" dump resources "$apk"
done

W="$OUT/aapt2"
mkdir -p "$W"
"$TOOLS/aapt2" compile --dir "$HERE/aapt2/res" -o "$W/res.zip"
"$TOOLS/aapt2" link -o "$W/linked.apk" -I "$JAR" --manifest "$HERE/aapt2/AndroidManifest.xml" "$W/res.zip"
(cd "$ROOT" && go run ./tools/apkcheck -rewrite "$W/linked.apk" -package org.yakbd.layout.fo -label "Føroyskt" -asset layout.json='{"t":1}' -o "$OUT/rewritten.apk")
apk="$OUT/rewritten.apk"
check rewritten-verify verified "$apk"
check rewritten-zipalign "$TOOLS/zipalign" -c -p -v 4 "$apk"
check rewritten-badging has "package: name='org.yakbd.layout.fo' versionCode='7'" "$TOOLS/aapt2" dump badging "$apk"
check rewritten-label has "application-label:'Føroyskt'" "$TOOLS/aapt2" dump badging "$apk"
check rewritten-permission has '"org.yakbd.layout.fo.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION"' "$TOOLS/aapt2" dump xmltree --file AndroidManifest.xml "$apk"
check rewritten-authority has '"org.yakbd.layout.fo.startup"' "$TOOLS/aapt2" dump xmltree --file AndroidManifest.xml "$apk"
check rewritten-class has '"org.yakbd.apkcheck.runtime.Ime"' "$TOOLS/aapt2" dump xmltree --file AndroidManifest.xml "$apk"
check rewritten-method has "supportsSwitchingToNextInputMethod" "$TOOLS/aapt2" dump xmltree --file res/xml/method.xml "$apk"
check rewritten-asset has "assets/layout.json" unzip -l "$apk"
exit $fail
