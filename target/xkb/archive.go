package xkb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"strings"
	"time"

	"github.com/yakbd/yakbd-cli/target/xkb/build"
)

// File is one file of a build, by its path under the build's folder.
type File struct {
	Path string
	Data []byte
	Exec bool
}

// Files are the files of a build (targets/xkb.md §5): xkb/symbols/N, xkb/rules/evdev.xml, with yakbd types
// xkb/types/<types> and xkb/rules/evdev.post, with dead keys compose/N.XCompose.
func Files(res *build.Result) []File {
	n := res.Pins.SymbolsName
	fs := []File{
		{Path: "xkb/symbols/" + n, Data: res.SymbolsText},
		{Path: "xkb/rules/evdev.xml", Data: res.Registry},
	}
	if res.Types != nil {
		fs = append(fs, File{Path: "xkb/types/" + res.TypesName, Data: res.Types}, File{Path: "xkb/rules/evdev.post", Data: res.Rules})
	}
	if res.ComposeText != nil {
		fs = append(fs, File{Path: "compose/" + n + ".XCompose", Data: res.ComposeText})
	}
	return fs
}

// Folder is the name of the archive's top folder and file: N, or N-V for a variant.
func Folder(res *build.Result) string {
	if v := build.VariantOf(res.Pins); v != "" {
		return res.Pins.SymbolsName + "-" + v
	}
	return res.Pins.SymbolsName
}

// Archive is the release form: <folder>.tar.gz with the build's files, install.sh and README under <folder>/.
// The archive is deterministic: fixed order, times and owners.
func Archive(res *build.Result) ([]byte, error) {
	top := Folder(res)
	fs := append(Files(res), File{Path: "install.sh", Data: InstallScript(res), Exec: true}, File{Path: "README", Data: Readme(res)})
	var buf bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	gz.ModTime = time.Unix(0, 0)
	tw := tar.NewWriter(gz)
	mtime := time.Unix(0, 0).UTC()
	dirs := map[string]bool{}
	for _, f := range fs {
		parts := strings.Split(top+"/"+f.Path, "/")
		for i := 1; i < len(parts); i++ {
			d := strings.Join(parts[:i], "/") + "/"
			if !dirs[d] {
				dirs[d] = true
				if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: d, Mode: 0o755, ModTime: mtime, Format: tar.FormatPAX}); err != nil {
					return nil, err
				}
			}
		}
		mode := int64(0o644)
		if f.Exec {
			mode = 0o755
		}
		h := &tar.Header{Typeflag: tar.TypeReg, Name: top + "/" + f.Path, Mode: mode, Size: int64(len(f.Data)), ModTime: mtime, Format: tar.FormatPAX}
		if err := tw.WriteHeader(h); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.Data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// shq quotes s for a POSIX shell.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Selection is how to select the layout, one line per desktop (targets/xkb.md §5).
func Selection(res *build.Result) []string {
	n, v := res.Pins.SymbolsName, build.VariantOf(res.Pins)
	gs := n
	sway := "input type:keyboard xkb_layout " + n
	kde := n
	x11 := "-layout " + n
	if v != "" {
		gs = n + "+" + v
		sway += ", then xkb_variant " + v
		kde = n + "(" + v + ")"
		x11 += " -variant " + v
	}
	types := ""
	if res.Types != nil {
		types = " -types complete+" + res.TypesName
	}
	return []string{
		fmt.Sprintf("GNOME: Settings > Keyboard > Input Sources > Add, or: gsettings set org.gnome.desktop.input-sources sources \"[('xkb', 'us'), ('xkb', '%s')]\"", gs),
		fmt.Sprintf("KDE Plasma: System Settings > Keyboard > Layouts > Add (the layout %s; LayoutList in ~/.config/kxkbrc)", kde),
		fmt.Sprintf("sway: in ~/.config/sway/config: %s; then swaymsg reload", sway),
		fmt.Sprintf("X11 session (does not read ~/.config/xkb): setxkbmap -I\"$HOME/.config/xkb\" %s%s -print | xkbcomp -I\"$HOME/.config/xkb\" - \"$DISPLAY\"", x11, types),
	}
}

// InstallScript is install.sh: install for the current user (targets/xkb.md §5), or remove with --uninstall.
func InstallScript(res *build.Result) []byte {
	n, v := res.Pins.SymbolsName, build.VariantOf(res.Pins)
	id := n // the marker blocks are named by the layout (targets/xkb.md §5)
	types := ""
	if res.Types != nil {
		types = res.TypesName
	}
	compose := ""
	if res.ComposeText != nil {
		compose = "compose/" + n + ".XCompose"
	}
	var s strings.Builder
	s.WriteString(`#!/bin/sh
# Installs the XKB layout below for the current user (no root), or removes it with --uninstall.
# Written by yakbd (targets/xkb.md §5). It changes only:
#   ~/.config/xkb/symbols/<name> (and types/<types>): copied, removed on uninstall;
#   ~/.config/xkb/rules/evdev.xml and evdev.post: one marked block for this layout;
#   the Compose file: a new ~/.XCompose, or one marked block in each Compose file you already have
#   (a copy of each such file is kept as <file>.yakbd-backup first).
set -eu
`)
	fmt.Fprintf(&s, "NAME=%s\nVARIANT=%s\nBLOCK=%s\nTYPES=%s\nCOMPOSE=%s\n", shq(n), shq(v), shq(id), shq(types), shq(compose))
	fmt.Fprintf(&s, "REGISTRY=%s\nRULES=%s\n", shq(build.RegistryLayout(res.Pins)), shq(string(res.Rules)))
	s.WriteString(`
HERE=$(cd "$(dirname "$0")" && pwd)
CONF=${XDG_CONFIG_HOME:-$HOME/.config}
XKB=$CONF/xkb

# strip FILE BEGIN END: remove the lines from BEGIN to END (the marked block) from FILE.
strip() {
	[ -f "$1" ] || return 0
	awk -v b="$2" -v e="$3" '$0 == b { skip = 1; next } $0 == e { skip = 0; next } !skip' "$1" > "$1.yakbd-tmp"
	# written back in place: a symbolic link and the file's mode stay
	cat "$1.yakbd-tmp" > "$1"
	rm -f "$1.yakbd-tmp"
}

# block FILE BEGIN END TEXT: replace the marked block of FILE by TEXT, at the end of the file.
block() {
	strip "$1" "$2" "$3"
	{ printf '%s\n' "$2"; printf '%s\n' "$4"; printf '%s\n' "$3"; } >> "$1"
}

composefiles() {
	for f in "$CONF/XCompose" "$HOME/.XCompose" "$CONF/gtk-4.0/Compose"; do
		[ -f "$f" ] && printf '%s\n' "$f"
	done
	[ -n "${XCOMPOSEFILE:-}" ] && printf '%s\n' "$XCOMPOSEFILE"
	return 0
}

CB="# yakbd $BLOCK begin"
CE="# yakbd $BLOCK end"
XB="<!-- yakbd $BLOCK begin -->"
XE="<!-- yakbd $BLOCK end -->"
RB="// yakbd $BLOCK begin"
RE="// yakbd $BLOCK end"

uninstall() {
	rm -f "$XKB/symbols/$NAME"
	[ -n "$TYPES" ] && rm -f "$XKB/types/$TYPES"
	strip "$XKB/rules/evdev.xml" "$XB" "$XE"
	strip "$XKB/rules/evdev.post" "$RB" "$RE"
	composefiles | while IFS= read -r f; do
		strip "$f" "$CB" "$CE"
		# a file this script created and that now holds nothing else
		if [ "$(head -n 1 "$f")" = "# Created by yakbd" ] && [ -z "$(grep -v '^#' "$f" | grep -v '^include "%L"$' | grep -v '^$' || true)" ]; then
			rm -f "$f"
		fi
	done
	echo "Removed the layout $BLOCK. Sign out and in again so that the settings apps forget it."
}

install() {
	mkdir -p "$XKB/symbols" "$XKB/rules"
	cp "$HERE/xkb/symbols/$NAME" "$XKB/symbols/$NAME"
	if [ -n "$TYPES" ]; then
		mkdir -p "$XKB/types"
		cp "$HERE/xkb/types/$TYPES" "$XKB/types/$TYPES"
	fi
	if [ ! -f "$XKB/rules/evdev.xml" ]; then
		printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>' '<!DOCTYPE xkbConfigRegistry SYSTEM "xkb.dtd">' \
			'<xkbConfigRegistry version="1.1">' '  <layoutList>' '  </layoutList>' '</xkbConfigRegistry>' > "$XKB/rules/evdev.xml"
	fi
	strip "$XKB/rules/evdev.xml" "$XB" "$XE"
	if ! grep -q '^[[:space:]]*</layoutList>[[:space:]]*$' "$XKB/rules/evdev.xml"; then
		echo "install.sh: $XKB/rules/evdev.xml has no line that is only </layoutList>; add the layout to it by hand:" >&2
		printf '%s\n' "$REGISTRY" >&2
		exit 1
	fi
	R="$REGISTRY" awk -v b="$XB" -v e="$XE" '!done && /^[[:space:]]*<\/layoutList>[[:space:]]*$/ { print b; printf "%s", ENVIRON["R"]; print e; done = 1 } { print }' \
		"$XKB/rules/evdev.xml" > "$XKB/rules/evdev.xml.yakbd-tmp"
	cat "$XKB/rules/evdev.xml.yakbd-tmp" > "$XKB/rules/evdev.xml"
	rm -f "$XKB/rules/evdev.xml.yakbd-tmp"
	if [ -n "$TYPES" ]; then
		touch "$XKB/rules/evdev.post"
		block "$XKB/rules/evdev.post" "$RB" "$RE" "$(printf '%s' "$RULES")"
	fi
	if [ -n "$COMPOSE" ]; then
		SEQ=$(grep -v '^#' "$HERE/$COMPOSE" | grep -v '^include ' || true)
		# Two targets (targets/xkb.md §5): the file libxkbcommon and Qt read, and the one GTK 4 reads.
		LXC=
		if [ -n "${XCOMPOSEFILE:-}" ]; then LXC=$XCOMPOSEFILE
		elif [ -f "$CONF/XCompose" ]; then LXC=$CONF/XCompose
		elif [ -f "$HOME/.XCompose" ]; then LXC=$HOME/.XCompose
		fi
		GTK=$LXC
		[ -f "$CONF/gtk-4.0/Compose" ] && GTK=$CONF/gtk-4.0/Compose
		for f in "$LXC" "$GTK"; do
			if [ -z "$f" ] || [ ! -f "$f" ]; then
				f=$HOME/.XCompose
				if [ ! -f "$f" ]; then
					{ echo "# Created by yakbd"; grep '^include ' "$HERE/$COMPOSE" || true; } > "$f"
				fi
			elif [ ! -f "$f.yakbd-backup" ] && [ "$(head -n 1 "$f")" != "# Created by yakbd" ]; then
				cp "$f" "$f.yakbd-backup"
			fi
			block "$f" "$CB" "$CE" "$SEQ"
			echo "Dead keys: in $f."
		done
	fi
	echo "Installed the layout $BLOCK for $(id -un). Select it:"
`)
	for _, l := range Selection(res) {
		fmt.Fprintf(&s, "\techo %s\n", shq("  "+l))
	}
	s.WriteString(`	echo "Sign out and in again if the settings apps do not list it yet. Remove it with: $0 --uninstall"
}

case "${1:-}" in
--uninstall) uninstall ;;
"") install ;;
*) echo "usage: $0 [--uninstall]" >&2; exit 2 ;;
esac
`)
	return []byte(s.String())
}

// Readme is the archive's README.
func Readme(res *build.Result) []byte {
	p := res.Pins
	var s strings.Builder
	fmt.Fprintf(&s, "%s: an XKB keyboard layout for Linux (Wayland and X11)\n\n", p.Description)
	fmt.Fprintf(&s, "Layout name: %s", p.SymbolsName)
	if v := build.VariantOf(p); v != "" {
		fmt.Fprintf(&s, ", variant %s", v)
	}
	s.WriteString("\n\nInstall for your user (no root):   ./install.sh\nRemove it again:                    ./install.sh --uninstall\n\n")
	s.WriteString("What install.sh changes:\n")
	fmt.Fprintf(&s, "- copies xkb/symbols/%s to ~/.config/xkb/symbols/ (libxkbcommon 1.7 and later read it from there)\n", p.SymbolsName)
	if res.Types != nil {
		fmt.Fprintf(&s, "- copies xkb/types/%s to ~/.config/xkb/types/ and adds a marked block to ~/.config/xkb/rules/evdev.post\n  (key types of this layout; read by libxkbcommon 1.13 and later, older versions give those keys a default type)\n", res.TypesName)
	}
	s.WriteString("- adds a marked block with the layout to ~/.config/xkb/rules/evdev.xml, so that the settings apps list it\n")
	if res.ComposeText != nil {
		s.WriteString("- dead keys: adds a marked block to the Compose file libxkbcommon and Qt read ($XCOMPOSEFILE, else\n" +
			"  ~/.config/XCompose or ~/.XCompose) and to the one GTK 4 reads (~/.config/gtk-4.0/Compose when it exists),\n" +
			"  keeping a copy as <file>.yakbd-backup; with no such file it writes ~/.XCompose. Programs read it when they start.\n" +
			"  GTK 4 programs on sway, KDE Plasma and GNOME without an input method drop dead keys unless\n" +
			"  GTK_IM_MODULE=simple is set; IBus and fcitx5 read Compose files of their own.\n")
	}
	s.WriteString("Uninstall removes exactly these files and blocks.\n\nSelecting the layout:\n")
	for _, l := range Selection(res) {
		s.WriteString("- " + l + "\n")
	}
	s.WriteString("\nThe settings apps may list the layout only after you sign out and in again.\n")
	if p.Characters == "utf8" {
		s.WriteString("\nThis layout writes characters as UTF-8 strings: it needs libxkbcommon 1.9 or later and does not load in X11 sessions.\n")
	}
	return []byte(s.String())
}
