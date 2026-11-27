package main

// Clean-machine check of a release (docs/release.md §3): the files a release workflow run built, as downloaded
// from its draft (bundle folder ci\), installed and used on a fresh guest. Phases (each a new scheduled task, so
// each sees the machine environment as a newly started program does):
//
//	clean-install    the files match SHA256SUMS; yakbd-cli-setup.exe /quiet /norestart; the uninstall entry
//	clean-use        yakbd on PATH prints the release version; convert, validate -windows, installer with that
//	                 yakbd; the layout installer installs, the layout types, it uninstalls; the installed
//	                 yakbd.exe is the x64 build inside the release's yakbd-cli-setup.exe
//	clean-uninstall  yakbd-cli-setup.exe /x: entry, PATH entry and folder gone

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yakbd/yakbd-cli/installer"
	"github.com/yakbd/yakbd-cli/installer/payload"
	"github.com/yakbd/yakbd-cli/installer/setup"
	"github.com/yakbd/yakbd-cli/installer/toolsetup"
)

var cleanPhases = []string{"clean-install", "clean-use", "clean-uninstall"}

// cleanLayout is the corpus layout the clean-use phase converts and installs (a copy in the bundle).
const cleanLayout = "kbdtst.klc"

const environmentKey = `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`

func (c *checker) ci(name string) string { return filepath.Join(c.bundle, "ci", name) }

func (c *checker) cleanVersion() string {
	b, _ := os.ReadFile(c.ci("VERSION.txt"))
	return strings.TrimSpace(string(b))
}

func toolDir() string { return filepath.Join(os.Getenv("ProgramW6432"), "YAKBD", "CLI") }

// rootDir is the installers' folder, Program Files\YAKBD.
func rootDir() string { return filepath.Join(os.Getenv("ProgramW6432"), "YAKBD") }

func (c *checker) clean(phase string) bool {
	switch phase {
	case "clean-install":
		c.cleanAssets()
		c.cleanInstall()
	case "clean-use":
		c.cleanOnPath()
		c.cleanLayoutFlow()
		c.cleanPayload()
	case "clean-uninstall":
		c.cleanUninstall()
	default:
		return false
	}
	return true
}

// cleanAssets: every file of ci\ named in SHA256SUMS has its hash.
func (c *checker) cleanAssets() {
	s := c.begin("clean-assets-match-sha256sums")
	b, err := os.ReadFile(c.ci("SHA256SUMS"))
	if err != nil {
		s.failf("%v", err)
		s.end()
		return
	}
	n := 0
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		sum, name, ok := strings.Cut(strings.TrimSpace(l), "  ")
		if !ok {
			continue
		}
		f, err := os.ReadFile(c.ci(name))
		if err != nil {
			continue // only the files the bundle carries
		}
		h := sha256.Sum256(f)
		if fmt.Sprintf("%x", h) != sum {
			s.failf("%s: hash differs", name)
		}
		n++
	}
	if n < 1 {
		s.failf("none of the bundle's files is in SHA256SUMS")
	}
	s.notef("%d files checked, version %s", n, c.cleanVersion())
	s.end()
}

func (c *checker) cleanInstall() {
	s := c.begin("clean-setup-quiet-installs")
	if code := c.runInstaller(s, c.ci("yakbd-cli-setup.exe"), "clean-setup-install", "/quiet", "/norestart"); code != setup.ExitOK {
		s.failf("exit code %d, want 0", code)
	}
	key := uninstallKey + `\` + installer.SuiteProductCode()
	if n := regString(hkeyLocalMachine, key, "DisplayName"); n == "" {
		s.failf("no Installed apps entry %s", installer.SuiteProductCode())
	} else if v := regString(hkeyLocalMachine, key, "DisplayVersion"); v != c.cleanVersion() {
		s.failf("entry %q has version %q, want %q", n, v, c.cleanVersion())
	} else {
		s.notef("entry %q %s", n, v)
	}
	if _, err := os.Stat(filepath.Join(toolDir(), "yakbd.exe")); err != nil {
		s.failf("%v", err)
	}
	if !pathHas(toolDir()) {
		s.failf("the machine Path does not name %s", toolDir())
	}
	s.end()
}

func pathHas(dir string) bool {
	for _, p := range strings.Split(regString(hkeyLocalMachine, environmentKey, "Path"), ";") {
		if strings.EqualFold(strings.TrimRight(strings.TrimSpace(p), `\`), dir) {
			return true
		}
	}
	return false
}

// shell runs a command line through cmd.exe, as a person types it in a new window, in dir.
func shell(dir, line string) (string, error) {
	cmd := exec.Command("cmd.exe", "/d", "/c", line)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (c *checker) cleanOnPath() {
	s := c.begin("clean-yakbd-on-path-prints-version")
	out, err := shell(os.TempDir(), "yakbd version")
	if want := "yakbd " + c.cleanVersion(); err != nil || out != want {
		s.failf("yakbd version: %v %q, want %q", err, out, want)
	}
	where, _ := shell(os.TempDir(), "where yakbd")
	if first, _, _ := strings.Cut(where, "\n"); !strings.EqualFold(strings.TrimSpace(first), filepath.Join(toolDir(), "yakbd.exe")) {
		s.failf("where yakbd: %q", where)
	}
	s.end()
}

// cleanLayoutFlow: the installed yakbd converts, validates and builds an installer from the corpus .klc; the
// layout installer installs and enables the layout, it types as its expectations say, and it uninstalls.
func (c *checker) cleanLayoutFlow() {
	work := filepath.Join(c.res, "work")
	_ = os.RemoveAll(work)
	_ = os.MkdirAll(work, 0o755)
	b, err := os.ReadFile(filepath.Join(c.bundle, cleanLayout))
	if err == nil {
		err = os.WriteFile(filepath.Join(work, cleanLayout), b, 0o644)
	}
	s := c.begin("clean-convert-validate-installer")
	if err != nil {
		s.failf("%v", err)
		s.end()
		return
	}
	for _, line := range []string{
		"yakbd convert -q kbdtst.klc kbdtst.kbl.json",
		"yakbd validate -q -windows kbdtst.kbl.json",
		"yakbd installer -q kbdtst.klc",
		"yakbd compile -q -emit-expectations -o exp kbdtst.klc",
	} {
		out, err := shell(work, line)
		fmt.Fprintf(c.detail, "  %s: %v\n%s\n", line, err, out)
		if err != nil {
			s.failf("%s: %v %s", line, err, lastLine(out))
		}
	}
	exe := filepath.Join(work, "kbdtst-setup.exe")
	pb, err := os.ReadFile(exe)
	var l *layout
	if err == nil {
		var p *payload.Payload
		if p, err = payload.FromExe(pb); err == nil {
			l = &layout{exe: exe, p: p, expect: filepath.Join(work, "exp", "kbdtst.expect.txt")}
		}
	}
	if err != nil {
		s.failf("kbdtst-setup.exe: %v", err)
	}
	s.end()
	if l == nil {
		return
	}

	s = c.begin("clean-layout-installs-types-uninstalls")
	if code := c.runInstaller(s, exe, "clean-layout-install", "/quiet", "/norestart"); code != setup.ExitOK {
		s.failf("install exit code %d", code)
	}
	klid, _ := c.checkInstalled(s, l, true)
	if klid != "" {
		if msg, ok := typeLayout(klid, l.expect, 0); !ok {
			s.failf("typing: %s", msg)
		} else {
			s.notef("KLID %s, %s", klid, msg)
		}
	}
	if code := c.runInstaller(s, exe, "clean-layout-uninstall", "/x", "/quiet", "/norestart"); code != setup.ExitOK && code != setup.ExitReboot {
		s.failf("uninstall exit code %d", code)
	}
	if regString(hkeyLocalMachine, uninstallKey+`\`+l.p.Manifest.Product.Code, "DisplayName") != "" {
		s.failf("the layout's entry is still there")
	}
	s.end()
}

func lastLine(s string) string {
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// cleanPayload: the installed yakbd.exe is the x64 build inside ci\yakbd-cli-setup.exe, and the installer copy beside it
// is ci\yakbd-cli-setup.exe (the release has no separate Windows archive of yakbd.exe since 0.1.0-preview.3).
func (c *checker) cleanPayload() {
	s := c.begin("clean-installed-yakbd-is-the-release-build")
	defer s.end()
	b, err := os.ReadFile(c.ci("yakbd-cli-setup.exe"))
	if err != nil {
		s.failf("%v", err)
		return
	}
	p, err := toolsetup.FromExe(b)
	if err != nil {
		s.failf("payload of ci\\yakbd-cli-setup.exe: %v", err)
		return
	}
	got, err := os.ReadFile(filepath.Join(toolDir(), "yakbd.exe"))
	if err != nil {
		s.failf("%v", err)
		return
	}
	files, err := p.FilesFor(toolsetup.CLI, toolsetup.BuildX64)
	var want []byte
	for _, f := range files {
		if f.Path == toolsetup.CLIExe {
			want = f.Data
		}
	}
	if err != nil || !bytes.Equal(got, want) {
		s.failf("the installed yakbd.exe is not the x64 build of the installer's payload")
	}
	if copyOf, err := os.ReadFile(filepath.Join(rootDir(), "setup.exe")); err != nil || !bytes.Equal(copyOf, b) {
		s.failf("setup.exe in the folder is not ci\\yakbd-cli-setup.exe (%v)", err)
	}
	h := sha256.Sum256(got)
	s.notef("yakbd.exe x64 %x, payload %s", h[:6], p.Manifest.Summary())
}

func (c *checker) cleanUninstall() {
	s := c.begin("clean-setup-uninstall-leaves-nothing")
	code := c.runInstaller(s, c.ci("yakbd-cli-setup.exe"), "clean-setup-uninstall", "/x", "/quiet", "/norestart")
	if code != setup.ExitOK {
		s.failf("exit code %d, want 0", code)
	}
	if regString(hkeyLocalMachine, uninstallKey+`\`+installer.SuiteProductCode(), "DisplayName") != "" {
		s.failf("the Installed apps entry is still there")
	}
	if pathHas(toolDir()) {
		s.failf("the machine Path still names %s", toolDir())
	}
	if _, err := os.Stat(rootDir()); err == nil {
		s.failf("%s still exists", rootDir())
	}
	s.end()
}
