#!/usr/bin/env bash
# Android check driver (README.md). Runs every case of cases/ against the one running emulator (adb) and
# writes OUTDIR/summary.txt: one line per case, "PASS name" or "FAIL name: reason", then timing lines.
#   run.sh OUTDIR APKDIR        APKDIR holds the APKs from build.sh
# Environment: ANDROID_CHECK_CASES (names separated by spaces; empty: all), ANDROID_CHECK_T0 (epoch seconds
# when the job started, for the timing line).
set -uo pipefail
OUT=${1:?usage: run.sh OUTDIR APKDIR}
APKS=${2:?usage: run.sh OUTDIR APKDIR}
HERE=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$OUT/cases"
OUT=$(cd "$OUT" && pwd)
SUMMARY="$OUT/summary.txt"
LOG="$OUT/log.txt"
: > "$SUMMARY"
: > "$LOG"
T1=$(date +%s)

PKG=org.yakbd.androidcheck
IME="$PKG/.CheckIme"
D=/data/local/tmp/yakbd

log() { echo "$(date +%T) $*" >> "$LOG"; }
sh_() { adb shell "$@" 2>&1; }
result() { echo "$1" >> "$SUMMARY"; log "$1"; }

wait_boot() {
  local i
  for i in $(seq 1 120); do
    [ "$(adb shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = 1 ] && return 0
    sleep 2
  done
  return 1
}

# Linux input key codes for the key names used in cases.
code() {
  case $1 in
    Q) echo 16 ;; W) echo 17 ;; E) echo 18 ;; A) echo 30 ;; SPACE) echo 57 ;;
    RALT) echo 100 ;; LSHIFT) echo 42 ;;
    *) echo "unknown key $1" >&2; return 1 ;;
  esac
}

# A uinput keyboard per case: uinput reads a device-side FIFO that a sleeping writer keeps open, so the device
# lives until the end of the run; commands are written to the FIFO when needed.
uin_register() { # name vid pid
  local name=$1 vid=$2 pid=$3 keys
  keys=$(seq -s, 1 127)
  sh_ "mkdir -p $D; rm -f $D/$name.fifo; mkfifo $D/$name.fifo" >> "$LOG"
  adb shell "nohup sh -c 'uinput - < $D/$name.fifo > $D/$name.log 2>&1' > /dev/null 2>&1 &"
  adb shell "nohup sh -c 'sleep 86400 > $D/$name.fifo' > /dev/null 2>&1 &"
  printf '{"id":1,"command":"register","name":"%s","vid":%d,"pid":%d,"bus":"usb","configuration":[{"type":100,"data":[1]},{"type":101,"data":[%s]}]}\n' \
    "$name" "$vid" "$pid" "$keys" > "$OUT/cases/$name.register.json"
  adb push "$OUT/cases/$name.register.json" "$D/$name.register.json" > /dev/null
  sh_ "cat $D/$name.register.json > $D/$name.fifo" >> "$LOG"
  local i
  for i in $(seq 1 20); do
    if adb shell dumpsys input 2>/dev/null | grep -qF "$name"; then sleep 2; return 0; fi
    sleep 1
  done
  return 1
}

uin_type() { # name keys...
  local name=$1; shift
  local ev="" tok k mods m
  for tok in "$@"; do
    mods=""
    k=$tok
    if [[ $tok == *+* ]]; then mods=${tok%+*}; k=${tok##*+}; fi
    for m in ${mods//+/ }; do ev+="1,$(code "$m"),1,0,0,0,"; done
    ev+="1,$(code "$k"),1,0,0,0,1,$(code "$k"),0,0,0,0,"
    for m in ${mods//+/ }; do ev+="1,$(code "$m"),0,0,0,0,"; done
  done
  printf '{"id":1,"command":"inject","events":[%s]}\n' "${ev%,}" > "$OUT/cases/$name.inject.json"
  adb push "$OUT/cases/$name.inject.json" "$D/$name.inject.json" > /dev/null
  sh_ "cat $D/$name.inject.json > $D/$name.fifo" >> "$LOG"
  sleep 2
}

clear_field() {
  sh_ am start -W -n "$PKG/.MainActivity" --ez clear true >> "$LOG"
  sleep 2
}

read_field() { adb shell run-as "$PKG" cat files/text.txt 2>/dev/null; }

layout_tool() { # action [devices [layout]]
  local extra=()
  [ -n "${2:-}" ] && extra+=(-e device "$2")
  [ -n "${3:-}" ] && extra+=(-e layout "$3")
  adb shell am instrument -w --no-hidden-api-checks -e action "$1" "${extra[@]}" "$PKG/.LayoutTool" 2>&1
}

restart_field() { # after the layout tool or a system restart: the app process is new
  sh_ ime enable "$IME" >> "$LOG"
  sh_ ime set "$IME" >> "$LOG"
  sh_ am start -W -n "$PKG/.MainActivity" >> "$LOG"
  sleep 3
  clear_field
}

hex2dec() { printf '%d' "$1"; }

# Root route: write a choice into input-manager-state.xml while the system is stopped. The entry is a copy of
# the one the api route stored for its keyboard (case fields from_device, from_pid, from_layout), with that
# keyboard's ids and layout replaced; without such an entry it is built from the key format of
# KeyboardLayoutManager (Android 15 and later).
root_select() { # name vid pid layoutdesc
  local name=$1 vid pid layout=$4 handle abx=0 f=/data/system/input-manager-state.xml
  local fpid fdesc ndesc
  vid=$(hex2dec "$2"); pid=$(hex2dec "$3")
  [ -s "$OUT/layouts.txt" ] || layout_tool dump > "$OUT/layouts.txt"
  if [ "$(sh_ "head -c 3 $f")" = ABX ]; then abx=1; sh_ "abx2xml $f $D/ims.xml" >> "$LOG"; else sh_ "cp $f $D/ims.xml" >> "$LOG"; fi
  adb pull "$D/ims.xml" "$OUT/ims-before.xml" > /dev/null 2>&1 || echo '<?xml version="1.0" encoding="utf-8" standalone="yes" ?><input-manager-state></input-manager-state>' > "$OUT/ims-before.xml"
  fdesc=$(cat "$OUT"/cases/*.tool.txt 2>/dev/null | grep -m1 "^device $from_device " | sed 's/.*descriptor=//')
  ndesc=$(layout_tool dump "$name" | grep -m1 "^device $name " | sed 's/.*descriptor=//')
  [ -n "$from_pid" ] && fpid=$(hex2dec "$from_pid")
  if [ -n "$from_pid" ] && grep -qF "<input-device descriptor=\"vendor:$vid,product:$fpid\">" "$OUT/ims-before.xml"; then
    awk -v src="<input-device descriptor=\"vendor:$vid,product:$fpid\"" -v dst="<input-device descriptor=\"vendor:$vid,product:$pid\"" \
        -v fdesc="$fdesc" -v ndesc="$ndesc" -v fl="$from_layout" -v nl="$layout" -v fk="vendor:$vid,product:$fpid" -v nk="vendor:$vid,product:$pid" '
      function sub_all(s, a, b,   out, i) { out = ""; while ((i = index(s, a)) > 0) { out = out substr(s, 1, i - 1) b; s = substr(s, i + length(a)) } return out s }
      FNR == NR { if (index($0, src) && !copying) { copying = 1 } if (copying) { blk = blk $0 "\n"; if (index($0, "</input-device>") || index($0, src "/>") || index($0, src " />")) copying = 0 } next }
      index($0, dst) { skip = 1 }
      skip { if (index($0, "</input-device>") || index($0, "/>") && index($0, dst)) { skip = 0 } next }
      index($0, "</input-devices>") { b = sub_all(blk, fk, nk); b = sub_all(b, fl, nl); if (fdesc != "" && ndesc != "") b = sub_all(b, fdesc, ndesc); printf "%s", b }
      { print }' "$OUT/ims-before.xml" "$OUT/ims-before.xml" > "$OUT/ims-after.xml"
  else
    handle=$(grep -o "subtypeHandle:[^\"]*" "$OUT/ims-before.xml" | head -n 1 | cut -d: -f2-)
    [ -n "$handle" ] || handle=$(grep "^subtype-handle " "$OUT/layouts.txt" | head -n 1 | cut -d" " -f2)
    [ -n "$handle" ] || { echo "no keyboard subtype handle"; return 1; }
    local dev="<input-device descriptor=\"vendor:$vid,product:$pid\""
    local keyed="<keyed-keyboard-layout key=\"layoutDescriptor:vendor:$vid,product:$pid,userId:0,subtypeHandle:$handle\" layout=\"$layout\" />"
    if grep -qF "$dev />" "$OUT/ims-before.xml"; then
      sed "s#$dev />#$dev>$keyed</input-device>#" "$OUT/ims-before.xml" > "$OUT/ims-after.xml"
    elif grep -qF "$dev>" "$OUT/ims-before.xml"; then
      sed "s#$dev>#$dev>$keyed#" "$OUT/ims-before.xml" > "$OUT/ims-after.xml"
    elif grep -q '</input-devices>' "$OUT/ims-before.xml"; then
      sed "s#</input-devices>#$dev>$keyed</input-device></input-devices>#" "$OUT/ims-before.xml" > "$OUT/ims-after.xml"
    else
      sed "s#</input-manager-state>#<input-devices>$dev>$keyed</input-device></input-devices></input-manager-state>#" "$OUT/ims-before.xml" > "$OUT/ims-after.xml"
    fi
  fi
  adb push "$OUT/ims-after.xml" "$D/ims-after.xml" > /dev/null
  sh_ stop >> "$LOG"
  sleep 3
  if [ $abx = 1 ]; then sh_ "xml2abx $D/ims-after.xml $f" >> "$LOG"; else sh_ "cp $D/ims-after.xml $f" >> "$LOG"; fi
  sh_ "rm -f $f.bak; chown system:system $f; chmod 600 $f; restorecon $f" >> "$LOG"
  sh_ setprop sys.boot_completed 0 >> "$LOG"
  sh_ start >> "$LOG"
  sleep 10
  wait_boot || { echo "system did not come back after restart"; return 1; }
  local i
  for i in $(seq 1 30); do
    adb shell pm path android > /dev/null 2>&1 && adb shell ime list -s 2>/dev/null | grep -qF "$IME" && break
    sleep 2
  done
  sleep 10
  return 0
}

# ---- setup ----
adb wait-for-device
wait_boot || { result "FAIL setup: emulator did not boot"; exit 1; }
T2=$(date +%s)
adb root >> "$LOG" 2>&1
sleep 3
adb wait-for-device
wait_boot
SDK=$(adb shell getprop ro.build.version.sdk | tr -d '\r')
log "sdk $SDK, $(adb shell getprop ro.build.fingerprint | tr -d '\r')"
sh_ settings put global hidden_api_policy 1 >> "$LOG"
adb logcat -G 16M >> "$LOG" 2>&1
sh_ settings put secure show_ime_with_hard_keyboard 1 >> "$LOG"
for apk in kcm-nocode kcm-nodex app; do
  adb install -r -t "$APKS/$apk.apk" > "$OUT/install-$apk.txt" 2>&1
  log "install $apk.apk: $(tr '\n' ' ' < "$OUT/install-$apk.txt")"
done
for i in $(seq 1 30); do
  adb shell ime list -a -s 2>/dev/null | grep -qxF "$IME" && break
  sleep 2
done
sh_ ime enable "$IME" >> "$LOG"
sh_ ime set "$IME" >> "$LOG"
sh_ am start -W -n "$PKG/.MainActivity" >> "$LOG"
sleep 3
T3=$(date +%s)

# ---- cases ----
sel="${ANDROID_CHECK_CASES:-}"
for cf in "$HERE"/cases/*.case; do
  name=$(basename "$cf" .case)
  if [ -n "$sel" ] && [[ " $sel " != *" $name "* ]]; then continue; fi
  kind="" apk="" package="" nocode="" install="" from_device="" from_pid="" from_layout="" missing="" device="" vid="" pid="" select="" layout="" keys="" taps="" expect="" expect_ime="" got="" ik=""
  while IFS='=' read -r k v; do
    case $k in ''|'#'*) continue ;; esac
    printf -v "$k" '%s' "$v"
  done < "$cf"
  log "== case $name ($kind)"
  reason=""
  case $kind in
    listed)
      if [ -n "$install" ]; then
        # A refusal is expected: PASS when the install failed with this text.
        grep -qF "$install" "$OUT/install-$apk.txt" || reason="install: $(tr "\n" " " < "$OUT/install-$apk.txt")"
        if [ -z "$reason" ]; then result "PASS $name (refused: $(grep -o "INSTALL_[A-Z_]*" "$OUT/install-$apk.txt" | head -n 1))"; fi
        [ -z "$reason" ] && continue
      fi
      grep -q Success "$OUT/install-$apk.txt" || reason="$apk.apk did not install: $(tr '\n' ' ' < "$OUT/install-$apk.txt")"
      sh_ dumpsys package "$package" > "$OUT/cases/$name.package.txt"
      if [ -z "$reason" ] && [ "$nocode" = yes ] && grep -q 'pkgFlags=.*HAS_CODE' "$OUT/cases/$name.package.txt"; then reason="package has HAS_CODE"; fi
      layout_tool dump > "$OUT/cases/$name.tool.txt"
      [ -s "$OUT/layouts.txt" ] || cp "$OUT/cases/$name.tool.txt" "$OUT/layouts.txt"
      missing=""
      for l in $expect; do grep -qxF "layout $l" "$OUT/cases/$name.tool.txt" || missing+=" $l"; done
      if [ -z "$reason" ] && [ -n "$missing" ]; then
        # Probe: a broadcast to the receiver takes the package out of the stopped state; listed then?
        sh_ am broadcast -a android.hardware.input.action.QUERY_KEYBOARD_LAYOUTS --include-stopped-packages -p "$package" >> "$LOG"
        sleep 2
        layout_tool dump > "$OUT/cases/$name.tool2.txt"
        after="not listed"
        for l in $missing; do grep -qxF "layout $l" "$OUT/cases/$name.tool2.txt" && after="listed"; done
        reason="not listed:$missing (receiver: $(grep -m1 "^receiver $package/" "$OUT/cases/$name.tool.txt" || echo none); after a broadcast: $after)"
      fi
      restart_field
      ;;
    tap)
      clear_field
      sh_ dumpsys input_method > "$OUT/cases/$name.ime.txt"
      keysfile=$(adb shell run-as "$PKG" cat files/keys.txt 2>/dev/null | tr -d '\r')
      echo "$keysfile" > "$OUT/cases/$name.keys.txt"
      for t in $taps; do
        line=$(echo "$keysfile" | grep "^$t " | head -n 1)
        if [ -z "$line" ]; then reason="key $t not reported by the keyboard"; break; fi
        read -r _ l tp r b <<< "$line"
        sh_ input tap $(( (l + r) / 2 )) $(( (tp + b) / 2 )) >> "$LOG"
        sleep 0.5
      done
      sleep 1
      got=$(read_field)
      ;;
    keyevent)
      clear_field
      # shellcheck disable=SC2086
      sh_ input keyevent $keys >> "$LOG"
      sleep 2
      got=$(read_field)
      ;;
    uinput|imehw)
      # The root route writes the choice before the keyboard first appears, then restarts the system.
      if [ "$select" = root ]; then
        rr=$(root_select "$device" "$vid" "$pid" "$layout") || reason="root route: $rr"
        restart_field
      fi
      [ -n "$reason" ] || uin_register "$device" "$vid" "$pid" || reason="uinput keyboard $device did not appear"
      if [ -z "$reason" ] && [ "$select" = api ]; then
        layout_tool set "$device" "$layout" > "$OUT/cases/$name.tool.txt"
        grep -q '^called setKeyboardLayoutForInputDevice' "$OUT/cases/$name.tool.txt" || reason="layout tool: $(grep -m1 -E 'failed|no set|error' "$OUT/cases/$name.tool.txt")"
        restart_field
      fi
      if [ -z "$reason" ]; then
        [ "$kind" = imehw ] && sh_ "rm -f /data/data/$PKG/files/ime-keys.txt" >> "$LOG"
        clear_field
        # shellcheck disable=SC2086
        uin_type "$device" $keys
        got=$(read_field)
        ik=$(adb shell run-as "$PKG" cat files/ime-keys.txt 2>/dev/null | tr -d '\r')
        echo "$ik" > "$OUT/cases/$name.ime-keys.txt"
        layout_tool dump "$device" > "$OUT/cases/$name.after.txt"
        restart_field
      fi
      ;;
    *) reason="unknown kind $kind" ;;
  esac
  if [ -z "$reason" ] && [ "$kind" != listed ]; then
    [ "$got" = "$expect" ] || reason="typed '$got', expected '$expect'"
    if [ -z "$reason" ] && [ "$kind" = imehw ]; then
      grep -qF "$expect_ime" <<< "$ik" || reason="keyboard saw: $(head -n 2 <<< "$ik" | tr '\n' ';')"
    fi
  fi
  if [ -z "$reason" ]; then result "PASS $name"; else result "FAIL $name: $reason"; fi
done

# ---- timing ----
T4=$(date +%s)
T0=${ANDROID_CHECK_T0:-$T1}
{
  echo "sdk $SDK"
  echo "time job-start-to-boot $(( T2 - T0 ))s, setup $(( T3 - T2 ))s, cases $(( T4 - T3 ))s, total $(( T4 - T0 ))s"
} >> "$SUMMARY"
sh_ dumpsys input > "$OUT/dumpsys-input.txt"
adb logcat -d > "$OUT/logcat.txt" 2>&1
adb shell "pkill -f 'sleep 86400'" > /dev/null 2>&1
cat "$SUMMARY"
exit 0
