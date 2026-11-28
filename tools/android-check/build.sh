#!/usr/bin/env bash
# Builds the two APKs of the Android check (README.md) into OUTDIR:
#   kcm.apk  the code-free keyboard-layout APK, with aapt2, zipalign and apksigner only (no Gradle, no dex);
#   app.apk  the check app (text field, check keyboard, layout tool), with Gradle.
# Both are signed with a key made for this run. Needs ANDROID_HOME, a JDK (keytool) and gradle on PATH.
set -euo pipefail
OUT=${1:?usage: build.sh OUTDIR}
HERE=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
PLATFORM=android-35
BT=$(ls "$ANDROID_HOME/build-tools" | sort -V | tail -n 1)
TOOLS="$ANDROID_HOME/build-tools/$BT"
JAR="$ANDROID_HOME/platforms/$PLATFORM/android.jar"
if [ ! -f "$JAR" ]; then
  yes | "$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager" "platforms;$PLATFORM" > "$OUT/sdkmanager.txt"
fi
echo "build-tools $BT, $PLATFORM" > "$OUT/build.txt"

# A key for this run only.
keytool -genkeypair -keystore "$OUT/check.jks" -storepass checkpass -keypass checkpass -alias check \
  -keyalg RSA -keysize 2048 -validity 3650 -dname "CN=yakbd android-check" > /dev/null 2>&1

W="$OUT/kcm-build"
mkdir -p "$W"
"$TOOLS/aapt2" compile --dir "$HERE/kcm/res" -o "$W/res.zip"
"$TOOLS/aapt2" link -o "$W/unaligned.apk" -I "$JAR" --manifest "$HERE/kcm/AndroidManifest.xml" \
  --min-sdk-version 34 --target-sdk-version 35 "$W/res.zip"
"$TOOLS/zipalign" -p -f 4 "$W/unaligned.apk" "$W/aligned.apk"
"$TOOLS/apksigner" sign --ks "$OUT/check.jks" --ks-pass pass:checkpass --ks-key-alias check \
  --out "$OUT/kcm.apk" "$W/aligned.apk"
{
  echo "== kcm.apk entries"
  unzip -l "$OUT/kcm.apk"
  echo "== kcm.apk manifest"
  "$TOOLS/aapt2" dump xmltree --file AndroidManifest.xml "$OUT/kcm.apk"
  echo "== kcm.apk signature"
  "$TOOLS/apksigner" verify --print-certs -v "$OUT/kcm.apk"
} >> "$OUT/build.txt" 2>&1

(cd "$HERE/app" && gradle --no-daemon -q assembleDebug) >> "$OUT/build.txt" 2>&1
"$TOOLS/apksigner" sign --ks "$OUT/check.jks" --ks-pass pass:checkpass --ks-key-alias check \
  --out "$OUT/app.apk" "$HERE/app/build/outputs/apk/debug/app-debug.apk"
echo "built $OUT/kcm.apk $OUT/app.apk"
