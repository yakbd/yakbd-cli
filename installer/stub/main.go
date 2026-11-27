// Command stub is the Windows program at the start of every installer that yakbd writes (docs/installer.md). It
// reads the payload after its own PE image, installs, upgrades, repairs or uninstalls the layout with
// installer/setup, and relaunches itself elevated when it has to. Its command line and exit codes are msiexec's
// (installer/cmdline, docs/installer-cli.md). It is built for windows/386 only, so that one program runs on x86,
// x64 and ARM64 Windows; `go run ./tools/buildstub` builds it into installer/stub-386.exe. It imports the standard
// library, installer/cmdline, installer/payload, installer/setup, installer/shape and installer/winhost (shape brings target/windows,
// pe and loss: a change in any of them needs go run ./tools/buildstub).
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/yakbd/yakbd-cli/installer/cmdline"
	"github.com/yakbd/yakbd-cli/installer/payload"
	"github.com/yakbd/yakbd-cli/installer/setup"
	"github.com/yakbd/yakbd-cli/installer/shape"
	"github.com/yakbd/yakbd-cli/installer/winhost"
)

// host is Windows as the program needs it: setup's System plus elevation, dialogs, the lock and the restart.
type host interface {
	setup.System
	// Relaunch starts this program again elevated (verb runas) with args, waits for it and returns its exit
	// code; errCancelled when the user declined the elevation prompt.
	Relaunch(args []string) (int, error)
	// SessionUser reports whether this process runs as the user of its interactive session; false after an
	// elevation with another account's credentials (inst section 4.3, over the shoulder) and for LocalSystem.
	SessionUser() (bool, error)
	// Message shows a dialog; with question it has Yes and No and returns true for Yes.
	Message(title, text string, kind winhost.MsgKind) bool
	// Lock takes the machine-wide lock of yakbd installers (docs/installer-cli.md §8); false when another one
	// holds it. It is held until the process ends.
	Lock() (bool, error)
	// Restart asks Windows to restart now (InitiateShutdownW, docs/installer-cli.md §5).
	Restart() error
}

const (
	msgInfo     = winhost.MsgInfo
	msgError    = winhost.MsgError
	msgQuestion = winhost.MsgQuestion
)

var errCancelled = winhost.ErrCancelled

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
			h.Message("yakbd setup", err.Error()+"\n\n"+cmdline.Usage("setup.exe"), msgError)
		}
		return codeOf(err)
	}
	if c.Action == cmdline.Help {
		h.Message("yakbd setup", cmdline.Usage("setup.exe"), msgInfo)
		return cmdline.Success
	}
	show := c.ShowsEnd() && !c.Elevated
	self := h.SelfPath()
	selfBytes, err := h.ReadFile(self)
	var p *payload.Payload
	if err == nil {
		p, err = payload.FromExe(selfBytes)
	}
	if err != nil {
		if show {
			h.Message("yakbd setup", "This installer is damaged and cannot be read: "+err.Error(), msgError)
		}
		return cmdline.PackageInvalid
	}
	m := p.Manifest
	title := m.Product.Name + " setup"
	def := h.TempDir() + `\yakbd-` + strings.TrimSuffix(strings.ToLower(m.Layout.File), ".dll") + "-setup.log"
	f, logPath, lerr := c.OpenLog(def)
	if lerr != nil {
		if show {
			h.Message(title, lerr.Error(), msgError)
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
	s := &setup.Setup{Sys: h, P: p, Log: logf, Sleep: time.Sleep, CheckFile: shape.Check}
	if c.SignInSet && c.Action != cmdline.Uninstall {
		v := c.SignIn
		s.SignIn = &v // SIGNIN=0|1 (docs/installer-cli.md §3); uninstall always cleans the places
	}
	enable := m.Enable
	if c.EnableSet {
		enable = c.Enable
	}
	enable = enable && c.Action != cmdline.Uninstall
	logf("start: %s, arguments %q, %s %s, payload %v, ui %s, enable %v", c.Action, args, m.Product.Name, m.Product.Version, m.Builds(), c.UI, enable)
	if len(c.Ignored) > 0 {
		logf("properties accepted without effect: %s", strings.Join(c.Ignored, ", "))
	}
	end := func(code int, text string) int {
		logf("done: exit code %d", code)
		if show && text != "" {
			kind := msgInfo
			if !cmdline.Succeeded(code) {
				kind = msgError
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
		q, err := payload.FromExe(b)
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
		logf("%s: the product is not installed", c.Action)
		return end(cmdline.UnknownProduct, fmt.Sprintf("\"%s\" is not installed; nothing was changed.", m.Product.Name))
	}

	if c.Asks() && !c.Elevated {
		q := fmt.Sprintf("Install the keyboard layout \"%s\" (version %s)?", m.Product.Name, m.Product.Version)
		switch c.Action {
		case cmdline.Uninstall:
			q = fmt.Sprintf("Remove the keyboard layout \"%s\"?", m.Product.Name)
		case cmdline.Repair:
			q = fmt.Sprintf("Repair the keyboard layout \"%s\"?", m.Product.Name)
		}
		if !h.Message(title, q, msgQuestion) {
			logf("cancelled by the user")
			return end(cmdline.UserExit, "")
		}
	}

	// Whether this process's user has the layout, and its Layout Id, before the machine part: a repair that has to
	// give the layout a new Layout Id enables it again for this user afterwards (kbl windows-install.md §5).
	before := s.Before()
	var notes []string
	code := cmdline.Success
	// signInFailed: the sign-in screen step failed (kbl targets/windows-install.md §7.1); the user's enabling still
	// runs, and the run ends with the enabling failure code.
	signInFailed := false
	// userDone: the user's part (re-enabling after a new Layout Id, enabling) ran already in the elevated part, which
	// is the user's own account (kbl targets/windows-install.md §7.1 "Order", D97).
	userDone := false
	enabled := false
	// userPart enables the layout for the user this process runs as (or says why not), after the machine part.
	userPart := func() {
		ok := cmdline.Succeeded(code)

		if ok && c.Action != cmdline.Uninstall && !(c.EnableSet && !c.Enable) {
			// The user's own process; ENABLE=0 leaves the user's list alone, also here.
			r, ran, err := s.Reenable(before)
			switch {
			case err != nil:
				logf("re-enable failed: %v", err)
				notes = append(notes, err.Error())
				code = setup.ExitEnable
			case ran:
				enabled = true
				notes = append(notes, r.Notes...)
			}
			if ran {
				enable = false // done
			}
		}
		if ok && enable {
			same, err := h.SessionUser()
			logf("session user check: same account %v (%v)", same, err)
			if err == nil && !same {
				// Enabling from this process would write another account's list (inst section 4.3); a deployment as
				// LocalSystem lands here too. The machine part is the installation: success (docs/installer-cli.md §4).
				logf("enable skipped: this process runs as another account than the session's user")
				notes = append(notes, "The layout is installed. The installer ran with another account's rights, so it did not add the layout to your input list: add it in Settings > Time & language > Language & region.")
				enable = false
			}
		}
		if ok && enable {
			r, err := s.Enable()
			if err != nil {
				logf("enable failed: %v", err)
				notes = append(notes, err.Error())
				code = setup.ExitEnable
			} else {
				enabled = true
				notes = append(notes, r.Notes...)
			}
		}
	}
	if !h.Elevated() {
		if c.Elevated || !c.MayPromptElevation() {
			logf("refused: not elevated, and the user interface level allows no elevation prompt")
			return end(cmdline.ElevationRequired, "Administrator rights are needed (run the installer elevated).")
		}
		// The machine part runs elevated; enabling stays in this process, the user's own (inst §4.3).
		res := h.TempDir() + `\yakbd-` + strings.Trim(m.Product.Code, "{}") + "-result.txt"
		_ = os.Remove(res)
		child := c.ChildArgs(res)
		if c.Log == "" {
			child = append(child, "/l*+", logPath)
		}
		logf("relaunching elevated: %q", child)
		if f != nil {
			f.Sync()
		}
		cc, err := h.Relaunch(child)
		if errors.Is(err, errCancelled) {
			logf("elevation declined")
			return end(cmdline.UserExit, "")
		}
		if err != nil {
			return end(cmdline.InstallFailure, fmt.Sprintf("Cannot start the installer with administrator rights: %v", err))
		}
		code = cc
		if b, err := os.ReadFile(res); err == nil {
			for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				if l == signInFailedMark {
					signInFailed = true
				} else if l == userDoneMark {
					userDone = true
				} else if l == enabledMark {
					userDone, enabled = true, true
				} else if l != "" {
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
		var r *setup.Result
		if c.Action == cmdline.Uninstall {
			r, err = s.Uninstall(c.Force)
		} else {
			r, err = s.Install()
		}
		if err != nil {
			code = setup.ExitCode(err)
			notes = append(notes, err.Error())
			logf("failed (exit code %d): %v", code, err)
		} else {
			if r.Reboot {
				code = cmdline.RebootRequired
			}
			if c.Action != cmdline.Uninstall {
				// D97: an elevated process of the session user's own account enables for that user first, then
				// writes the sign-in screen places (whose language tag may come from the user's hive). A child of
				// another account (over the shoulder) leaves the user's part to the first process.
				same, serr := h.SessionUser()
				if serr == nil && same || !c.Elevated {
					userPart()
					userDone = true
				}
				s.SignInStep(r)
				if r.SignInFailed != nil {
					signInFailed = true
					logf("sign-in screen step failed: %v", r.SignInFailed)
				}
			}
			notes = append(notes, r.Notes...)
		}
		if c.Result != "" {
			out := notes
			if userDone && enabled {
				out = append([]string{enabledMark}, out...)
			} else if userDone {
				out = append([]string{userDoneMark}, out...)
			}
			if signInFailed {
				out = append([]string{signInFailedMark}, out...)
			}
			_ = os.WriteFile(c.Result, []byte(strings.Join(out, "\n")+"\n"), 0o644)
		}
	}
	if c.Elevated {
		logf("done (elevated part): exit code %d", code)
		return code
	}

	if !userDone {
		userPart()
	}
	if signInFailed && cmdline.Succeeded(code) {
		code = setup.ExitEnable
	}
	ok := cmdline.Succeeded(code)
	text := strings.Join(notes, "\n\n")
	switch {
	case ok && c.Action == cmdline.Uninstall:
		text = strings.TrimSpace(fmt.Sprintf("\"%s\" was removed.\n\n%s", m.Product.Name, text))
	case ok:
		how := "To use it, add it in Settings > Time & language > Language & region (the keyboards of your language)."
		if enabled {
			how = "It is in your input list: switch with Windows key + Space."
		}
		text = strings.TrimSpace(fmt.Sprintf("\"%s\" is installed.\n\n%s\n\n%s", m.Product.Name, how, text))
	}
	if ok {
		code = restart(c, h, code, logf, title)
	}
	return end(code, text)
}

// restart applies REBOOT and REBOOTPROMPT at the end of a successful run (docs/installer-cli.md §5).
func restart(c *cmdline.Command, h host, code int, logf func(string, ...any), title string) int {
	switch c.RestartDecision(code == cmdline.RebootRequired) {
	case cmdline.NoRestart:
		return code
	case cmdline.AskRestart:
		if !h.Message(title, "Windows must restart to finish. Restart now?", msgQuestion) {
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

// signInFailedMark is the first line of the elevated copy's result file when the sign-in screen step failed
// (setup.Result.SignInFailed): the first process then still enables and ends with the enabling failure code.
const signInFailedMark = "#yakbd: sign-in screen step failed"

// userDoneMark is a line of the elevated copy's result file: the copy is the session user's own account and has
// enabled the layout for the user already (D97), so the first process does not enable again.
const userDoneMark = "#yakbd: user part done"

// enabledMark is userDoneMark when the layout is in the user's list now.
const enabledMark = "#yakbd: user part done, enabled"
