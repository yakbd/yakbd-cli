#!/bin/bash
# Runs inside a test container as the user "tester": types every case of every layout
# under one stack and prints one result line per case:
#   STACK CLIENT LAYOUTDIR CASE PASS|FAIL|SKIP reason
#
# usage: inside.sh STACK [CLIENT...]
#   STACK  distro | versions | x11 | weston | sway | kwin | mutter | gnome-shell
#   CLIENT xkbtype (distro) | gtk | qt
#
# /xkb-check is this folder (read-only), /opt/xkb-check/bin the helpers built in the image.
# MIT-0.
set -u
# It rewrites the home folder's XKB and Compose files: run it only in the test images.
if [ ! -d /opt/xkb-check/bin ] || [ "$(id -un)" != tester ]; then
  echo "inside.sh: run it in an xkb-check image as the user tester (see run.sh)" >&2
  exit 2
fi
STACK=$1; shift
CLIENTS=${*:-gtk qt}
ROOT=/xkb-check
BIN=/opt/xkb-check/bin
WORK=$(mktemp -d)
export LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8
export XDG_RUNTIME_DIR=$WORK/xdg-runtime XDG_CONFIG_HOME=$HOME/.config
mkdir -p "$XDG_RUNTIME_DIR"; chmod 700 "$XDG_RUNTIME_DIR"
TIMEOUT=${CASE_TIMEOUT:-40}

declare -A CODE
while read -r name code; do
  case $name in ''|'#'*) continue ;; esac
  CODE[$name]=$code
done < "$ROOT/keycodes.tsv"

# keys_to_events "TOKENS": d/u event lines for one case.
keys_to_events() {
  local t n
  for t in $1; do
    case $t in
      +\<*\>) n=${t#+<}; n=${n%>}; echo "d ${CODE[$n]}" ;;
      -\<*\>) n=${t#-<}; n=${n%>}; echo "u ${CODE[$n]}" ;;
      \<*\>)  n=${t#<}; n=${n%>}; echo "d ${CODE[$n]}"; echo "u ${CODE[$n]}" ;;
    esac
  done
}

# install_home LAYOUTDIR SCENARIO: the layout in the user path, the Compose files of the scenario.
install_home() {
  local dir=$1 scen=$2
  rm -rf "$HOME/.config/xkb" "$HOME/.XCompose" "$HOME/.config/XCompose" "$HOME/.config/gtk-4.0"
  mkdir -p "$HOME/.config/gtk-4.0"
  cp -r "$dir/xkb" "$HOME/.config/xkb"
  cp "$dir/compose/XCompose" "$HOME/.XCompose"
  if [ "$scen" = probe ]; then
    cp "$dir/compose/config-XCompose" "$HOME/.config/XCompose"
    cp "$dir/compose/gtk4-Compose" "$HOME/.config/gtk-4.0/Compose"
  fi
}

# configure LAYOUT: tell the stack which layout to use (compositor configuration files).
configure() {
  local l=$1
  mkdir -p "$HOME/.config/sway"
  printf 'output * resolution 800x600\ninput * xkb_layout %s\n' "$l" > "$HOME/.config/sway/config"
  printf '[core]\nbackend=x11\n[keyboard]\nkeymap_layout=%s\n[shell]\npanel-position=none\n' "$l" > "$HOME/.config/weston.ini"
  printf '[Layout]\nLayoutList=%s\nUse=true\n' "$l" > "$HOME/.config/kxkbrc"
}

wait_for() { # wait_for SECONDS TEST...
  local n=$(( $1 * 10 )); shift
  while [ $n -gt 0 ]; do "$@" && return 0; sleep 0.1; n=$((n - 1)); done
  return 1
}

PIDS=()
stop_all() {
  local p
  for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null; done
  sleep 0.5
  for p in "${PIDS[@]}"; do kill -9 "$p" 2>/dev/null; done
  PIDS=()
  pkill -u tester -x Xwayland 2>/dev/null
  rm -f /tmp/.X1-lock /tmp/.X11-unix/X1
}

start_xvfb() {
  Xvfb :1 -screen 0 1024x768x24 -nolisten tcp -noreset >"$LOG/xvfb.log" 2>&1 & PIDS+=($!)
  wait_for 10 test -e /tmp/.X11-unix/X1
}

# start_stack LAYOUT: start the display stack; sets INJECT (the injector command) and the client env.
start_stack() {
  local l=$1
  unset WAYLAND_DISPLAY DISPLAY GDK_BACKEND QT_QPA_PLATFORM
  case $STACK in
    x11)
      start_xvfb || return 1
      export DISPLAY=:1
      setxkbmap -I"$HOME/.config/xkb" -layout "$l" -print 2>>"$LOG/stack.log" \
        | xkbcomp -w0 -I"$HOME/.config/xkb" - :1 >>"$LOG/stack.log" 2>&1 || return 2
      export GDK_BACKEND=x11 QT_QPA_PLATFORM=xcb
      INJECT="$BIN/xtestkeys" ;;
    weston|sway)
      start_xvfb || return 1
      # The nested backends take the modifier state from the X server, so the X server needs the
      # same modifier keys as the layout: Right Alt as Level 3 (all test layouts use it).
      DISPLAY=:1 setxkbmap -layout us -option lv3:ralt_switch >"$LOG/xsetup.log" 2>&1
      case $STACK in
        weston) DISPLAY=:1 "$BIN/xtestkeys" names "$l"
                DISPLAY=:1 weston --backend=x11 --socket=wl-test >"$LOG/stack.log" 2>&1 & PIDS+=($!) ;;
        sway)   DISPLAY=:1 WLR_BACKENDS=x11 WLR_RENDERER=pixman WAYLAND_DISPLAY= sway >"$LOG/stack.log" 2>&1 & PIDS+=($!) ;;
      esac
      if [ "$STACK" = sway ]; then
        wait_for 15 sh -c 'ls "$XDG_RUNTIME_DIR"/wayland-[0-9] >/dev/null 2>&1' || return 1
        WAYLAND_DISPLAY=$(basename "$(ls "$XDG_RUNTIME_DIR"/wayland-[0-9] | head -1)")
      else
        wait_for 15 test -e "$XDG_RUNTIME_DIR/wl-test" || return 1
        WAYLAND_DISPLAY=wl-test
      fi
      export WAYLAND_DISPLAY
      export GDK_BACKEND=wayland QT_QPA_PLATFORM=wayland
      INJECT="env DISPLAY=:1 $BIN/xtestkeys" ;;
    kwin)
      # The nested X11 backend of KWin needs DRI3, which Xvfb lacks: use the virtual backend
      # and KWin's fake-input protocol.
      QT_FORCE_STDERR_LOGGING=1 kwin_wayland --virtual --socket wl-test >"$LOG/stack.log" 2>&1 & PIDS+=($!)
      wait_for 20 test -e "$XDG_RUNTIME_DIR/wl-test" || return 1
      export WAYLAND_DISPLAY=wl-test GDK_BACKEND=wayland QT_QPA_PLATFORM=wayland
      INJECT="$BIN/kwinkeys" ;;
    mutter|gnome-shell)
      # Both need a system bus; a private one is enough.
      if [ -z "${DBUS_SYSTEM_BUS_ADDRESS:-}" ]; then
        dbus-daemon --session --address="unix:path=$XDG_RUNTIME_DIR/system_bus" --fork >/dev/null
        export DBUS_SYSTEM_BUS_ADDRESS="unix:path=$XDG_RUNTIME_DIR/system_bus"
      fi
      if [ "$STACK" = mutter ]; then
        # Bare mutter takes the layout from systemd-localed's X11Layout; localed answers it.
        "$BIN/localed" "$l" >"$LOG/localed.log" 2>&1 & PIDS+=($!)
        wait_for 10 grep -q ready "$LOG/localed.log" || return 1
        mutter --headless --wayland --no-x11 --wayland-display wl-test --virtual-monitor 1024x768 \
          >"$LOG/stack.log" 2>&1 & PIDS+=($!)
      else
        gsettings set org.gnome.desktop.input-sources sources "[('xkb', '$l')]"
        gnome-shell --headless --wayland --no-x11 --wayland-display wl-test --virtual-monitor 1024x768 \
          >"$LOG/stack.log" 2>&1 & PIDS+=($!)
      fi
      wait_for 30 test -e "$XDG_RUNTIME_DIR/wl-test" || return 1
      wait_for 20 gdbus introspect --session --dest org.gnome.Mutter.RemoteDesktop \
        --object-path /org/gnome/Mutter/RemoteDesktop >/dev/null 2>&1 || return 1
      export WAYLAND_DISPLAY=wl-test GDK_BACKEND=wayland QT_QPA_PLATFORM=wayland
      INJECT="$BIN/rdkeys" ;;
  esac
  sleep 1
  return 0
}

# start_client CLIENT OUT: start a readback client; wait until it has the focus.
start_client() {
  local c=$1 out=$2 w=20
  case $STACK in mutter|gnome-shell) w=3 ;; esac
  rm -f "$out" "$out.ready"
  case $c in
    gtk) "$BIN/gtkread" "$out" >"$LOG/client.log" 2>&1 & PIDS+=($!)
         wait_for $w client_ready "test -e $out.ready" ;;
    qt)  QT_FORCE_STDERR_LOGGING=1 /usr/lib/qt6/bin/qml "$BIN/qtread.qml" >"$LOG/client.log" 2>&1 & PIDS+=($!)
         wait_for $w client_ready "grep -q 'XKBREAD READY' $LOG/client.log" ;;
  esac
  [ $? -eq 0 ] && return 0
  # A headless mutter has no keyboard, so no window gets the keyboard focus, until a
  # remote-desktop session adds one: open one short session (it taps Left Shift), wait again.
  case $STACK in
    mutter|gnome-shell) echo "s 200" | "$BIN/rdkeys" >/dev/null 2>&1 ;;
    *) return 1 ;;
  esac
  case $c in
    gtk) wait_for 10 test -e "$out.ready" ;;
    qt)  wait_for 10 grep -q 'XKBREAD READY' "$LOG/client.log" ;;
  esac
}

# client_ready TEST: with no window manager nothing gives the client window the focus,
# so on the X server (plain X11, or the nested compositor's window) focus the top window first.
client_ready() {
  case $STACK in
    x11|weston|sway) DISPLAY=:1 "$BIN/xtestkeys" focus 2>/dev/null ;;
  esac
  sh -c "$1"
}

# read_client CLIENT OUT: the text the client holds now.
read_client() {
  case $1 in
    gtk) cat "$2" 2>/dev/null ;;
    qt)  local t; t=$(grep -a 'XKBREAD TEXT ' "$LOG/client.log" | tail -1 | sed 's/.*XKBREAD TEXT //')
         printf '%b' "$(printf '%s' "$t" | sed 's/%\([0-9A-Fa-f][0-9A-Fa-f]\)/\\x\1/g')" ;;
  esac
}

# check_group LAYOUTDIR CLIENT LAYOUT SCENARIO TEXTFILE: one line per case of the group.
check_group() {
  local dir=$1 client=$2 l=$3 scen=$4 got=$5 i=0 id lay sc keys exp line
  local -a lines
  mapfile -t lines < "$got"
  while IFS=$'\t' read -r id lay sc keys exp; do
    case $id in ''|'#'*) continue ;; esac
    [ "$lay" = "$l" ] && [ "$sc" = "$scen" ] || continue
    line=${lines[$i]-}
    i=$((i + 1))
    if [ "$line" = "$exp" ]; then
      echo "$STACK $client $(basename "$dir") $id PASS"
    else
      echo "$STACK $client $(basename "$dir") $id FAIL expected '$exp' got '$line'"
    fi
  done < "$dir/cases.tsv"
}

# fail_group LAYOUTDIR CLIENT LAYOUT SCENARIO REASON
fail_group() {
  local id lay sc keys exp
  while IFS=$'\t' read -r id lay sc keys exp; do
    case $id in ''|'#'*) continue ;; esac
    [ "$lay" = "$3" ] && [ "$sc" = "$4" ] || continue
    echo "$STACK $2 $(basename "$1") $id FAIL $5"
  done < "$1/cases.tsv"
}

group_events() { # group_events LAYOUTDIR LAYOUT SCENARIO
  local id lay sc keys exp
  while IFS=$'\t' read -r id lay sc keys exp; do
    case $id in ''|'#'*) continue ;; esac
    [ "$lay" = "$2" ] && [ "$sc" = "$3" ] || continue
    { keys_to_events "$keys"; keys_to_events "<RTRN>"; } |
      if [ -n "${KEY_GAP_MS:-}" ]; then sed "s/\$/\\ns $KEY_GAP_MS/"; else cat; fi
    echo "s 100"
  done < "$1/cases.tsv"
}

run_group() { # run_group LAYOUTDIR CLIENT LAYOUT SCENARIO
  local dir=$1 client=$2 l=$3 scen=$4 out=$WORK/out.txt rc
  LOG=$WORK/log/$(basename "$dir")-$client-$l-$scen; mkdir -p "$LOG"
  install_home "$dir" "$scen"
  configure "$l"
  if [ "$STACK" = distro ]; then
    group_events "$dir" "$l" "$scen" | timeout "$TIMEOUT" "$BIN/xkbtype" "$l" >"$out" 2>"$LOG/client.log"
    rc=$?
    if [ $rc -ne 0 ]; then fail_group "$dir" "$client" "$l" "$scen" "xkbtype exit $rc: $(head -c 200 "$LOG/client.log" | tr '\n' ' ')"; return; fi
    check_group "$dir" "$client" "$l" "$scen" "$out"; return
  fi
  start_stack "$l"; rc=$?
  if [ $rc -ne 0 ]; then
    fail_group "$dir" "$client" "$l" "$scen" "stack did not start (rc $rc): $(tail -c 300 "$LOG/stack.log" | tr '\n' ' ')"
    stop_all; return
  fi
  if ! start_client "$client" "$out"; then
    fail_group "$dir" "$client" "$l" "$scen" "client not ready: $(tail -c 300 "$LOG/client.log" | tr '\n' ' ')"
    stop_all; return
  fi
  group_events "$dir" "$l" "$scen" | timeout "$TIMEOUT" $INJECT >>"$LOG/inject.log" 2>&1
  rc=$?
  sleep 1
  read_client "$client" "$out" > "$out.final"
  stop_all
  if [ $rc -ne 0 ]; then
    fail_group "$dir" "$client" "$l" "$scen" "injector exit $rc: $(tail -c 300 "$LOG/inject.log" | tr '\n' ' ')"; return
  fi
  check_group "$dir" "$client" "$l" "$scen" "$out.final"
}

# xkbcli_checks LAYOUTDIR: the layout is listed from the user evdev.xml, compiles, and
# `xkbcli how-to-type` finds the key of every one-key, one-character case.
xkbcli_checks() {
  local dir=$1 name=$(basename "$1") l id lay sc keys exp t out
  install_home "$dir" build
  for l in $(ls "$dir/xkb/symbols"); do
    if xkbcli list 2>/dev/null | grep -q "^ *- *layout: *'$l'"; then
      echo "$STACK xkbcli $name list-$l PASS"
    else
      echo "$STACK xkbcli $name list-$l FAIL not listed by xkbcli list"
    fi
    if out=$(xkbcli compile-keymap --layout "$l" 2>&1 >/dev/null) && [ -z "$(printf '%s' "$out" | grep -i error)" ]; then
      echo "$STACK xkbcli $name compile-$l PASS"
    else
      echo "$STACK xkbcli $name compile-$l FAIL $(printf '%s' "$out" | head -c 200 | tr '\n' ' ')"
    fi
  done
  while IFS=$'\t' read -r id lay sc keys exp; do
    case $id in ''|'#'*) continue ;; esac
    [ -e "$dir/xkb/symbols/$lay" ] && [ "${#exp}" -eq 1 ] || continue
    t=$(printf '%s\n' $keys | grep -v '^[+-]' | grep -v '<CAPS>')
    [ "$(printf '%s\n' "$t" | wc -l)" -eq 1 ] || continue
    t=${t#<}; t=${t%>}
    # A decimal code point: xkbcli 1.7 reads only that form.
    if xkbcli how-to-type --layout "$lay" "$(printf '%d' "'$exp")" 2>/dev/null | grep -qw "$t"; then
      echo "$STACK xkbcli $name how-to-type-$id PASS"
    else
      echo "$STACK xkbcli $name how-to-type-$id FAIL key $t not named for '$exp'"
    fi
  done < "$dir/cases.tsv"
}

# versions: one line with the distribution and the versions that matter.
versions() {
  local os pk p v line
  os=$(. /etc/os-release; echo "$PRETTY_NAME" | tr ' ' '_')
  line="version $os"
  for p in libxkbcommon xkeyboard-config libx11 weston sway kwin mutter gnome-shell gtk4 qt6-base xorg-server-xvfb; do
    v=
    if command -v pacman >/dev/null; then v=$(pacman -Q "$p" 2>/dev/null | cut -d' ' -f2)
    elif command -v rpm >/dev/null && ! command -v dpkg >/dev/null; then
      case $p in libx11) pk=libX11 ;; *) pk=$p ;; esac
      v=$(rpm -q --qf '%{VERSION}-%{RELEASE}' "$pk" 2>/dev/null) || v=
    else
      case $p in libxkbcommon) pk=libxkbcommon0 ;; xkeyboard-config) pk=xkb-data ;; libx11) pk=libx11-6 ;; *) pk=$p ;; esac
      v=$(dpkg-query -W -f '${Version}' "$pk" 2>/dev/null) || v=
    fi
    [ -n "$v" ] && line="$line $p=$v"
  done
  echo "$line"
}

main() {
  local dir client l scen
  if [ "$STACK" = versions ]; then versions; return; fi
  if [ "$STACK" = distro ]; then
    CLIENTS=xkbtype
    for dir in "$ROOT"/layouts/*/; do xkbcli_checks "${dir%/}"; done
  fi
  for dir in "$ROOT"/layouts/*/; do
    dir=${dir%/}
    for client in $CLIENTS; do
      awk -F'\t' '!/^#/ && NF >= 5 && !seen[$2 FS $3]++ { print $2 "\t" $3 }' "$dir/cases.tsv" |
      while IFS=$'\t' read -r l scen; do
        run_group "$dir" "$client" "$l" "$scen" </dev/null
      done
    done
  done
  if [ -n "${KEEP_LOGS:-}" ]; then mkdir -p "$KEEP_LOGS" && cp -r "$WORK/log/." "$KEEP_LOGS/" 2>/dev/null; fi
}

if [ "$STACK" != distro ] && [ "$STACK" != versions ] && [ -z "${DBUS_SESSION_BUS_ADDRESS:-}" ]; then
  exec dbus-run-session -- "$0" "$STACK" "$@"
fi
main
