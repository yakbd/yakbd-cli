#!/bin/sh
# Builds the helper programs inside an image: "distro" (typer only) or "desktop" (all).
set -eu
mkdir -p ../bin
cc -O2 -Wall -o ../bin/xkbtype xkbtype.c $(pkg-config --cflags --libs xkbcommon)
[ "$1" = distro ] && exit 0
cc -O2 -Wall -o ../bin/xtestkeys xtestkeys.c $(pkg-config --cflags --libs x11 xtst)
cc -O2 -Wall -o ../bin/gtkread gtkread.c $(pkg-config --cflags --libs gtk4)
cc -O2 -Wall -o ../bin/rdkeys rdkeys.c $(pkg-config --cflags --libs gio-2.0)
cc -O2 -Wall -o ../bin/localed localed.c $(pkg-config --cflags --libs gio-2.0)
# KWin's fake-input protocol, generated from the distribution's copy of the XML.
xml=$(find /usr/share -name fake-input.xml | head -1)
gen=$(mktemp -d)
wayland-scanner client-header "$xml" "$gen/fake-input-client-protocol.h"
wayland-scanner private-code "$xml" "$gen/fake-input-protocol.c"
cc -O2 -Wall -I"$gen" -o ../bin/kwinkeys kwinkeys.c "$gen/fake-input-protocol.c" $(pkg-config --cflags --libs wayland-client)
cp qtread.qml ../bin/
