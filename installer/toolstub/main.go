// Command toolstub is the program of the YAKBD installers, yakbd-cli-setup.exe and yakbd-gui-setup.exe
// (docs/installer-cli.md §12): it reads the tool payload after its PE image and installs, changes, repairs or
// uninstalls the components with installer/toolsetup, taking msiexec's command line and the installers' properties
// and giving Windows Installer's exit codes (installer/cmdline). With the full user interface (no /q option) it
// shows the wizard (wizard_windows.go) in its first, unelevated process, then starts its elevated copy with the
// properties the wizard set. It is built for windows/386 by `go run ./tools/toolsetup`, which appends the
// resources and the payload.
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
	// User is the starting user's side, for the settings (this process's user).
	User() toolsetup.UserSystem
	// Wizard shows the wizard and runs exec when its main button is pressed; it returns the exit code exec gave, or
	// 1602 when the user cancelled.
	Wizard(w *toolsetup.Wizard, exec func(req toolsetup.Request) (int, []string)) int
}

const title = "YAKBD setup"

func codeOf(err error) int {
	var e *cmdline.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return cmdline.InstallFailure
}

func run(args []string, h host) int {
	rest, req, perr := toolsetup.TakeProps(args)
	var c *cmdline.Command
	var err error
	if perr != nil {
		err = perr
	} else {
		c, err = cmdline.Parse(rest)
	}
	if err != nil {
		if !quietIn(args) {
			h.Message(title, err.Error()+"\n\n"+cmdline.Usage("yakbd-cli-setup.exe")+"\n\n"+propsUsage, winhost.MsgError)
		}
		return codeOf(err)
	}
	if c.Action == cmdline.Help {
		h.Message(title, cmdline.Usage("yakbd-cli-setup.exe")+"\n\n"+propsUsage, winhost.MsgInfo)
		return cmdline.Success
	}
	req.Uninstall = req.Uninstall || c.Action == cmdline.Uninstall
	req.Repair = c.Action == cmdline.Repair
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
	s := &toolsetup.Setup{Sys: h, P: p, Self: selfBytes, Log: logf}
	logf("start: %s, arguments %q, payload %s, ui %s", c.Action, args, m.Summary(), c.UI)
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
	st, err := s.Detect()
	if err != nil {
		return end(cmdline.InstallFailure, err.Error())
	}
	build, err := s.Machine()
	if err != nil {
		return end(codeOf(err), err.Error())
	}
	if err := p.Verify(build); err != nil {
		logf("damaged: %v", err)
		return end(cmdline.PackageInvalid, "This installer is damaged and cannot be read: "+err.Error())
	}
	logf("installed: %s; machine %s", describe(st), build)
	us := h.User()

	// exec runs a request: in this process when it is elevated, else in the elevated copy; then the starting user's
	// settings, in this process (never in the elevated copy, which may be another account).
	exec := func(req toolsetup.Request) (int, []string) {
		plan, err := s.Decide(st, build, req)
		if err != nil {
			logf("refused: %v", err)
			return codeOf(err), []string{err.Error()}
		}
		var notes []string
		code := cmdline.Success
		if !h.Elevated() {
			if c.Elevated || !c.MayPromptElevation() {
				logf("refused: not elevated, and the user interface level allows no elevation prompt")
				return cmdline.ElevationRequired, []string{"Administrator rights are needed (run the installer elevated)."}
			}
			res := h.TempDir() + `\yakbd-setup-` + strings.Trim(m.Product.Code, "{}") + "-result.txt"
			_ = os.Remove(res)
			child := childArgs(c, req, res)
			if c.Log == "" {
				child = append(child, "/l*+", logPath)
			}
			logf("relaunching elevated: %q", child)
			cc, err := h.Relaunch(child)
			if errors.Is(err, winhost.ErrCancelled) {
				logf("elevation declined")
				return cmdline.UserExit, nil
			}
			if err != nil {
				return cmdline.InstallFailure, []string{fmt.Sprintf("Cannot start the installer with administrator rights: %v", err)}
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
				logf("refused: another YAKBD installer is running")
				return cmdline.AlreadyRunning, []string{"Another YAKBD installer is running. Try again when it has finished."}
			}
			r, err := s.Apply(st, plan)
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
		gone := map[string]bool{}
		for _, id := range toolsetup.ComponentIDs {
			gone[id] = !plan.Final(id)
		}
		decl := append(append([]toolsetup.UserData(nil), st.UserData...), m.UserData...)
		if cmdline.Succeeded(code) && plan.RemoveUserData && !c.Elevated && toolsetup.HasUserData(us, decl, gone) {
			if err := toolsetup.RemoveUserData(us, decl, gone, logf); err != nil {
				notes = append(notes, err.Error())
			} else if toolsetup.HasUserData(us, decl, gone) {
				notes = append(notes, "Some settings could not be removed.")
			} else {
				notes = append(notes, "Your settings were removed.")
			}
		}
		return code, notes
	}

	if c.Asks() && !c.Elevated {
		decl := append(append([]toolsetup.UserData(nil), st.UserData...), m.UserData...)
		w, err := toolsetup.NewWizard(s, st, build, req.Uninstall, toolsetup.HasUserData(us, decl, map[string]bool{toolsetup.Editor: true}))
		if err != nil {
			return end(codeOf(err), err.Error())
		}
		code := h.Wizard(w, exec)
		logf("wizard: exit code %d", code)
		if cmdline.Succeeded(code) {
			code = restart(c, h, code, logf)
		}
		return code
	}
	code, notes := exec(req)
	if c.Elevated {
		logf("done (elevated part): exit code %d", code)
		return code
	}
	text := strings.Join(notes, "\n\n")
	if cmdline.Succeeded(code) {
		if req.Uninstall {
			text = strings.TrimSpace("YAKBD was removed.\n\n" + text)
		} else {
			text = strings.TrimSpace("YAKBD is installed. Open a new terminal and run: yakbd version\n\n" + text)
		}
		code = restart(c, h, code, logf)
	}
	return end(code, text)
}

// childArgs is the elevated copy's command line for a request: the action is the request's, not this command line's
// (the uninstall wizard, started with /x, may ask to remove the editor only), then the request as properties.
func childArgs(c *cmdline.Command, req toolsetup.Request, result string) []string {
	ec := *c
	switch {
	case req.Uninstall:
		ec.Action = cmdline.Uninstall
	case req.Repair:
		ec.Action = cmdline.Repair
	default:
		ec.Action = cmdline.Install
	}
	return append(ec.ChildArgs(result), toolsetup.PropArgs(req)...)
}

const propsUsage = `Properties of the YAKBD installers:
  ADDLOCAL=CLI|Editor|CLI,Editor   components to have installed
  REMOVE=Editor|ALL                remove the editor, or everything
  ADDTOPATH=0|1                    YAKBD CLI on the machine PATH
  REMOVEUSERDATA=0|1               remove your editor settings with the editor`

func describe(st *toolsetup.State) string {
	var out []string
	for _, id := range toolsetup.ComponentIDs {
		if st.Has(id) {
			out = append(out, id+" "+st.VersionOf(id))
		}
	}
	if st.Legacy != nil {
		out = append(out, "earlier layout")
	}
	if len(out) == 0 {
		return "nothing"
	}
	return strings.Join(out, ", ")
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
