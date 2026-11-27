// Command toolstub is yakbd-setup.exe, the tool's own installer (docs/installer-cli.md §12): it reads the tool
// payload after its PE image and installs, upgrades, repairs or uninstalls yakbd.exe with installer/toolsetup,
// taking msiexec's command line and giving Windows Installer's exit codes (installer/cmdline). It is built for
// windows/386 by `go run ./tools/toolsetup`, which appends the resources and the payload.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/yakbd/yakbd-cli/installer/cmdline"
	"github.com/yakbd/yakbd-cli/installer/toolsetup"
	"github.com/yakbd/yakbd-cli/installer/winhost"
)

// host is Windows as the program needs it.
type host interface {
	toolsetup.System
	Relaunch(args []string) (int, error)
	Message(title, text string, kind winhost.MsgKind) bool
	Lock() (bool, error)
	Restart() error
}

const title = "yakbd setup"

func codeOf(err error) int {
	var e *cmdline.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return cmdline.InstallFailure
}

func run(args []string, h host) int {
	c, err := cmdline.Parse(args)
	if err != nil {
		if !quietIn(args) {
			h.Message(title, err.Error()+"\n\n"+cmdline.Usage("yakbd-setup.exe"), winhost.MsgError)
		}
		return codeOf(err)
	}
	if c.Action == cmdline.Help {
		h.Message(title, cmdline.Usage("yakbd-setup.exe"), winhost.MsgInfo)
		return cmdline.Success
	}
	show := c.ShowsEnd() && !c.Elevated
	self := h.SelfPath()
	selfBytes, err := h.ReadFile(self)
	var p *toolsetup.Payload
	if err == nil {
		p, err = toolsetup.FromExe(selfBytes)
	}
	if err != nil {
		if show {
			h.Message(title, "This installer is damaged and cannot be read: "+err.Error(), winhost.MsgError)
		}
		return cmdline.PackageInvalid
	}
	m := p.Manifest
	f, logPath, lerr := c.OpenLog(h.TempDir() + `\yakbd-setup.log`)
	if lerr != nil {
		if show {
			h.Message(title, lerr.Error(), winhost.MsgError)
		}
		return lerr.Code
	}
	var logw io.Writer = io.Discard
	if f != nil {
		defer f.Close()
		logw = f
	}
	logf := func(format string, a ...any) {
		fmt.Fprintf(logw, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
	}
	s := &toolsetup.Setup{Sys: h, P: p, Log: logf}
	logf("start: %s, arguments %q, yakbd %s, payload %v, ui %s", c.Action, args, m.Product.Version, m.Builds(), c.UI)
	if len(c.Ignored) > 0 {
		logf("properties accepted without effect: %s", strings.Join(c.Ignored, ", "))
	}
	end := func(code int, text string) int {
		logf("done: exit code %d", code)
		if show && text != "" {
			kind := winhost.MsgInfo
			if !cmdline.Succeeded(code) {
				kind = winhost.MsgError
				text = strings.TrimSpace(text + "\n\nDetails: " + logPath)
			}
			h.Message(title, text, kind)
		}
		return code
	}
	productOf := func(path string) (string, error) {
		b, err := h.ReadFile(path)
		if err != nil {
			return "", err
		}
		q, err := toolsetup.FromExe(b)
		if err != nil {
			return "", err
		}
		return q.Manifest.Product.Code, nil
	}
	if err := c.CheckOperand(self, m.Product.Code, productOf); err != nil {
		logf("refused: %v", err)
		return end(err.Code, err.Error())
	}
	if (c.Action == cmdline.Uninstall || c.Action == cmdline.Repair) && !s.Installed() {
		logf("%s: yakbd is not installed", c.Action)
		return end(cmdline.UnknownProduct, "yakbd is not installed; nothing was changed.")
	}
	if c.Asks() && !c.Elevated {
		q := fmt.Sprintf("Install yakbd %s for all users (Program Files, on the PATH)?", m.Product.Version)
		switch c.Action {
		case cmdline.Uninstall:
			q = "Remove yakbd?"
		case cmdline.Repair:
			q = "Repair yakbd?"
		}
		if !h.Message(title, q, winhost.MsgQuestion) {
			logf("cancelled by the user")
			return end(cmdline.UserExit, "")
		}
	}

	var notes []string
	code := cmdline.Success
	if !h.Elevated() {
		if c.Elevated || !c.MayPromptElevation() {
			logf("refused: not elevated, and the user interface level allows no elevation prompt")
			return end(cmdline.ElevationRequired, "Administrator rights are needed (run the installer elevated).")
		}
		res := h.TempDir() + `\yakbd-setup-` + strings.Trim(m.Product.Code, "{}") + "-result.txt"
		_ = os.Remove(res)
		child := c.ChildArgs(res)
		if c.Log == "" {
			child = append(child, "/l*+", logPath)
		}
		logf("relaunching elevated: %q", child)
		cc, err := h.Relaunch(child)
		if errors.Is(err, winhost.ErrCancelled) {
			logf("elevation declined")
			return end(cmdline.UserExit, "")
		}
		if err != nil {
			return end(cmdline.InstallFailure, fmt.Sprintf("Cannot start the installer with administrator rights: %v", err))
		}
		code = cc
		if b, err := os.ReadFile(res); err == nil {
			for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				if l != "" {
					notes = append(notes, l)
				}
			}
		}
		_ = os.Remove(res)
	} else {
		ok, err := h.Lock()
		if err != nil {
			logf("lock: %v", err)
		}
		if !ok && err == nil {
			logf("refused: another yakbd installer is running")
			return end(cmdline.AlreadyRunning, "Another yakbd installer is running. Try again when it has finished.")
		}
		var r *toolsetup.Result
		if c.Action == cmdline.Uninstall {
			r, err = s.Uninstall()
		} else {
			r, err = s.Install()
		}
		if err != nil {
			code = toolsetup.ExitCode(err)
			notes = append(notes, err.Error())
			logf("failed (exit code %d): %v", code, err)
		} else {
			notes = append(notes, r.Notes...)
			if r.Reboot {
				code = cmdline.RebootRequired
			}
		}
		if c.Result != "" {
			_ = os.WriteFile(c.Result, []byte(strings.Join(notes, "\n")+"\n"), 0o644)
		}
	}
	if c.Elevated {
		logf("done (elevated part): exit code %d", code)
		return code
	}
	text := strings.Join(notes, "\n\n")
	if cmdline.Succeeded(code) {
		if c.Action == cmdline.Uninstall {
			text = strings.TrimSpace("yakbd was removed.\n\n" + text)
		} else {
			text = strings.TrimSpace(fmt.Sprintf("yakbd %s is installed. Open a new terminal and run: yakbd version\n\n%s", m.Product.Version, text))
		}
		code = restart(c, h, code, logf)
	}
	return end(code, text)
}

// restart applies REBOOT and REBOOTPROMPT at the end of a successful run (docs/installer-cli.md §5).
func restart(c *cmdline.Command, h host, code int, logf func(string, ...any)) int {
	switch c.RestartDecision(code == cmdline.RebootRequired) {
	case cmdline.NoRestart:
		return code
	case cmdline.AskRestart:
		if !h.Message(title, "Windows must restart to finish. Restart now?", winhost.MsgQuestion) {
			logf("restart: declined by the user")
			return cmdline.RebootRequired
		}
	}
	logf("restart: initiating a restart (exit code 1641)")
	if err := h.Restart(); err != nil {
		logf("restart failed: %v", err)
		return cmdline.RebootRequired
	}
	return cmdline.RebootInitiated
}

// quietIn reports whether a command line that does not parse asks for no UI.
func quietIn(args []string) bool {
	for _, a := range args {
		l := strings.ToLower(strings.TrimLeft(a, "/-"))
		if l == "quiet" || l == "passive" || l == "elevated" || strings.HasPrefix(l, "q") && len(l) <= 4 {
			return true
		}
	}
	return false
}
