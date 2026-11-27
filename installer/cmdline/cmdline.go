// Package cmdline is the command line and the exit codes of every installer program yakbd makes: the layout
// installers (installer/stub) and the YAKBD installers yakbd-cli-setup.exe and yakbd-gui-setup.exe (installer/toolstub). It follows msiexec's
// command line and Windows Installer's exit codes (docs/installer-cli.md, which cites the Microsoft pages each rule
// comes from): a deployment tool that knows how to drive an .msi drives these .exe files the same way. The package
// is pure (standard library only, no Windows calls), so that the whole contract is tested on any OS; it is
// compiled into the installer programs, so a change here needs `go run ./tools/buildstub`.
package cmdline

import (
	"fmt"
	"strings"
)

// Exit codes (docs/installer-cli.md §4). The numbers and names are Windows Installer's, from "MsiExec.exe and
// InstMsi.exe error messages" (https://learn.microsoft.com/en-us/windows/win32/msi/error-codes) and the system
// error codes pages.
const (
	Success             = 0    // ERROR_SUCCESS
	ElevationRequired   = 740  // ERROR_ELEVATION_REQUIRED: no UI allowed and not elevated
	UserExit            = 1602 // ERROR_INSTALL_USEREXIT: the user said no (our question or the elevation prompt)
	InstallFailure      = 1603 // ERROR_INSTALL_FAILURE: failed, rolled back (or refused with nothing changed)
	UnknownProduct      = 1605 // ERROR_UNKNOWN_PRODUCT: uninstall or repair of a product that is not installed
	AlreadyRunning      = 1618 // ERROR_INSTALL_ALREADY_RUNNING: another yakbd installer is running
	PackageOpenFailed   = 1619 // ERROR_INSTALL_PACKAGE_OPEN_FAILED: the package named on the command line
	PackageInvalid      = 1620 // ERROR_INSTALL_PACKAGE_INVALID: this installer is damaged
	LogFailure          = 1622 // ERROR_INSTALL_LOG_FAILURE: the /l file cannot be opened
	TransformFailure    = 1624 // ERROR_INSTALL_TRANSFORM_FAILURE: transforms are not supported
	PlatformUnsupported = 1633 // ERROR_INSTALL_PLATFORM_UNSUPPORTED: no build for this machine
	PatchOpenFailed     = 1635 // ERROR_PATCH_PACKAGE_OPEN_FAILED: patches are not supported
	ProductVersion      = 1638 // ERROR_PRODUCT_VERSION: a newer version is installed
	InvalidCommandLine  = 1639 // ERROR_INVALID_COMMAND_LINE
	RebootInitiated     = 1641 // ERROR_SUCCESS_REBOOT_INITIATED
	RebootRequired      = 3010 // ERROR_SUCCESS_REBOOT_REQUIRED
)

// Succeeded reports whether an exit code means success (0, 1641 and 3010, as the error-codes page says).
func Succeeded(code int) bool {
	return code == Success || code == RebootInitiated || code == RebootRequired
}

// Action is what a run does.
type Action int

// The actions.
const (
	Install   Action = iota // /i, /package, or no action switch: install, upgrade in place, or repair the same version
	Uninstall               // /x, /uninstall, REMOVE=ALL
	Repair                  // /f, REINSTALL=ALL
	Help                    // /?, /h, /help
)

func (a Action) String() string {
	return [...]string{"install", "uninstall", "repair", "help"}[a]
}

// UI is the user-interface level (/q...).
type UI int

// The levels of msiexec's /q option.
const (
	UIFull    UI = iota // the default and /qf: our question first, a dialog at the end
	UIReduced           // /qr: no question, no dialog at the end
	UIBasic             // /qb, /passive: no question; a dialog at the end only with "+"
	UINone              // /q, /qn, /quiet: nothing on the screen; with "+" a dialog at the end
)

func (u UI) String() string { return [...]string{"full", "reduced", "basic", "none"}[u] }

// Reboot is the REBOOT property (/norestart, /forcerestart).
type Reboot int

// REBOOT values; only the first character counts (https://learn.microsoft.com/en-us/windows/win32/msi/reboot).
const (
	RebootDefault  Reboot = iota // restart at the end when a file waits for the restart
	RebootSuppress               // S or R (Suppress, ReallySuppress, /norestart): never; exit code 3010 instead
	RebootForce                  // F (Force, /forcerestart): always restart at the end
)

// Prompt is the REBOOTPROMPT property (/passive, /promptrestart).
type Prompt int

// REBOOTPROMPT values.
const (
	PromptDefault  Prompt = iota // ask, when the UI level shows anything; restart without asking otherwise
	PromptSuppress               // S (/passive): restart without asking
	PromptAlways                 // /promptrestart (REBOOTPROMPT=""): ask; not allowed with no UI
)

// Command is a parsed command line.
type Command struct {
	Action Action
	// Operand is the package path or product code given after /i, /x, /f, /package or /uninstall; "" = this
	// program. CheckOperand decides whether it names this program's product.
	Operand string
	UI      UI
	// EndDialog is the "+" modifier of /qn+ and /qb+.
	EndDialog bool
	Reboot    Reboot
	Prompt    Prompt
	// Log is the /l or /log file, "" when not given (then the program logs to its default file, appending).
	Log string
	// LogFlags are the letters of /l (empty: /l alone, "iwearmo" in msiexec); LogAppend is "+".
	LogFlags  string
	LogAppend bool
	// RepairMode is the letters of /f or REINSTALLMODE (empty: /f alone).
	RepairMode string
	// Enable is ENABLE=0|1 (layout installers): EnableSet tells whether it was given.
	Enable, EnableSet bool
	// Force is FORCE=1: uninstall even when the layout is a user's only layout.
	Force bool
	// SignIn is SIGNIN=0|1 (layout installers): put the layout on the sign-in screen and into the Default profile
	// (kbl targets/windows-install.md §7.1, D87); SignInSet tells whether it was given. Uninstall ignores it.
	SignIn, SignInSet bool
	// Props are all properties given, by upper-case name, as given.
	Props map[string]string
	// Ignored names the properties accepted without effect (for the log).
	Ignored []string
	// Elevated and Result are internal: the elevated copy the program starts of itself, and the file it writes
	// its notes into for the first process (docs/installer.md §3).
	Elevated bool
	Result   string

	state
}

// Error is a command-line refusal with its exit code.
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func refuse(code int, format string, a ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// Repair letters of /f and REINSTALLMODE (https://learn.microsoft.com/en-us/windows/win32/msi/command-line-options).
const repairLetters = "podecamusv"

// Logging letters of /l (the same page); "+" appends, "!" flushes every line, "*" is every letter but v and x.
const logLetters = "iwearucmopvx+!*"

func (c *Command) setAction(a Action, operand string, opt string) *Error {
	// /i with REINSTALL=ALL or REMOVE=ALL is that property's action (msiexec /i x.msi REINSTALL=ALL, REMOVE=ALL).
	if a == Install && c.propAction {
		a = c.Action
	} else if c.actionSet && c.Action != a {
		return refuse(InvalidCommandLine, "%s cannot be combined with %s", opt, c.actionOpt)
	}
	if operand != "" {
		if c.Operand != "" && !strings.EqualFold(c.Operand, operand) {
			return refuse(InvalidCommandLine, "two packages named: %q and %q", c.Operand, operand)
		}
		c.Operand = operand
	}
	if !c.actionSet {
		c.actionOpt = opt
	}
	c.Action, c.actionSet = a, true
	return nil
}

// propertyAction applies REINSTALL=ALL (repair) or REMOVE=ALL (uninstall): allowed alone or with /i.
func (c *Command) propertyAction(a Action, prop string) *Error {
	if c.actionSet && c.Action != Install && c.Action != a {
		return refuse(InvalidCommandLine, "%s cannot be combined with %s", prop, c.actionOpt)
	}
	if !c.actionSet {
		c.actionOpt = prop
	}
	c.Action, c.actionSet, c.propAction = a, true, true
	return nil
}

// The parser's private state lives in the Command (not exported).
type state struct {
	actionSet  bool
	propAction bool
	actionOpt  string
	rebootOpt  string
}

// Parse reads the arguments (without the program name). Option names are case-insensitive and start with "/" or
// "-"; PROPERTY=value arguments set properties.
func Parse(args []string) (*Command, error) {
	c := &Command{Props: map[string]string{}}
	pendingOperand := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "" {
			continue
		}
		if arg[0] != '/' && arg[0] != '-' {
			if name, _, ok := strings.Cut(arg, "="); pendingOperand && !(ok && validPropertyName(name)) {
				if err := c.setAction(c.Action, arg, c.actionOpt); err != nil {
					return nil, err
				}
				pendingOperand = false
				continue
			}
			if name, value, ok := strings.Cut(arg, "="); ok && validPropertyName(name) {
				if err := c.property(strings.ToUpper(name), value); err != nil {
					return nil, err
				}
				continue
			}
			return nil, refuse(InvalidCommandLine, "unexpected argument %q", arg)
		}
		pendingOperand = false
		opt := arg[1:]
		if opt == "" {
			return nil, refuse(InvalidCommandLine, "unknown option %s", arg) // a lone / or -
		}
		low := strings.ToLower(opt)
		next := func() (string, *Error) {
			if i+1 >= len(args) {
				return "", refuse(InvalidCommandLine, "%s needs a file name", arg)
			}
			i++
			return args[i], nil
		}
		switch low {
		case "?", "h", "help":
			c.Action, c.actionSet = Help, true
			continue
		case "quiet":
			c.UI, c.EndDialog = UINone, false
			continue
		case "passive":
			c.UI, c.EndDialog, c.Prompt = UIBasic, false, PromptSuppress
			continue
		case "norestart":
			if err := c.setReboot(RebootSuppress, arg); err != nil {
				return nil, err
			}
			continue
		case "forcerestart":
			if err := c.setReboot(RebootForce, arg); err != nil {
				return nil, err
			}
			continue
		case "promptrestart":
			c.Prompt = PromptAlways
			continue
		case "package", "uninstall":
			a := Install
			if low == "uninstall" {
				a = Uninstall
			}
			if err := c.setAction(a, "", arg); err != nil {
				return nil, err
			}
			pendingOperand = true
			continue
		case "log":
			f, err := next()
			if err != nil {
				return nil, err
			}
			c.Log, c.LogFlags, c.LogAppend = f, "*", false
			continue
		case "update":
			return nil, refuse(PatchOpenFailed, "%s: this installer takes no patches; a new version is a new installer", arg)
		case "elevated":
			c.Elevated = true
			continue
		case "result":
			f, err := next()
			if err != nil {
				return nil, err
			}
			c.Result = f
			continue
		}
		rest := opt[1:]
		switch low[0] {
		case 'i', 'x':
			a := Install
			if low[0] == 'x' {
				a = Uninstall
			}
			if rest != "" && rest[0] != '{' {
				return nil, refuse(InvalidCommandLine, "unknown option %s", arg)
			}
			if err := c.setAction(a, rest, arg); err != nil {
				return nil, err
			}
			pendingOperand = rest == ""
		case 'f':
			letters, code, found := strings.Cut(rest, "{")
			if found {
				code = "{" + code
			}
			if err := checkLetters(letters, repairLetters, arg); err != nil {
				return nil, err
			}
			if err := c.setAction(Repair, code, arg); err != nil {
				return nil, err
			}
			c.RepairMode = strings.ToLower(letters)
			pendingOperand = code == ""
		case 'q':
			if err := c.setUI(strings.ToLower(rest), arg); err != nil {
				return nil, err
			}
		case 'l':
			if err := checkLetters(rest, logLetters, arg); err != nil {
				return nil, err
			}
			f, err := next()
			if err != nil {
				return nil, err
			}
			flags := strings.ToLower(rest)
			c.Log, c.LogAppend = f, strings.Contains(flags, "+")
			c.LogFlags = strings.NewReplacer("+", "", "!", "").Replace(flags)
		case 'p':
			if rest == "" {
				return nil, refuse(PatchOpenFailed, "%s: this installer takes no patches; a new version is a new installer", arg)
			}
			return nil, refuse(InvalidCommandLine, "unknown option %s", arg)
		case 't':
			return nil, refuse(TransformFailure, "%s: this installer takes no transforms", arg)
		case 'a':
			if rest == "" {
				return nil, refuse(InvalidCommandLine, "%s: there is no administrative installation of this installer", arg)
			}
			return nil, refuse(InvalidCommandLine, "unknown option %s", arg)
		case 'j':
			return nil, refuse(InvalidCommandLine, "%s: this installer cannot advertise", arg)
		case 'y', 'z', 'c', 'g', 'n', 'm':
			return nil, refuse(InvalidCommandLine, "%s is not supported by this installer", arg)
		default:
			return nil, refuse(InvalidCommandLine, "unknown option %s", arg)
		}
	}
	if err := c.finish(); err != nil {
		return nil, err
	}
	if c.Prompt == PromptAlways && c.UI == UINone {
		return nil, refuse(InvalidCommandLine, "/promptrestart cannot be combined with /quiet or /qn")
	}
	return c, nil
}

func checkLetters(s, allowed, opt string) *Error {
	for _, r := range strings.ToLower(s) {
		if !strings.ContainsRune(allowed, r) {
			return refuse(InvalidCommandLine, "%s: unknown letter %q", opt, r)
		}
	}
	return nil
}

func (c *Command) setReboot(r Reboot, opt string) *Error {
	if c.rebootOpt != "" && c.Reboot != r {
		return refuse(InvalidCommandLine, "%s cannot be combined with %s", opt, c.rebootOpt)
	}
	c.Reboot, c.rebootOpt = r, opt
	return nil
}

// setUI reads the letters after /q: n, b, r or f (none = n), then the modifiers + - ! of the command-line-options
// page (+ with n or b, - and ! with b only; + and - not together).
func (c *Command) setUI(s, opt string) *Error {
	level, mods := "n", s
	if s != "" && strings.ContainsRune("nbrf", rune(s[0])) {
		level, mods = s[:1], s[1:]
	}
	ui := map[string]UI{"n": UINone, "b": UIBasic, "r": UIReduced, "f": UIFull}[level]
	plus := false
	seen := map[rune]bool{}
	for _, m := range mods {
		if seen[m] {
			return refuse(InvalidCommandLine, "%s is not a user interface level", opt)
		}
		seen[m] = true
		switch {
		case m == '+' && (level == "n" || level == "b"):
			plus = true
		case (m == '-' || m == '!') && level == "b":
		default:
			return refuse(InvalidCommandLine, "%s is not a user interface level", opt)
		}
	}
	if seen['+'] && seen['-'] {
		return refuse(InvalidCommandLine, "%s is not a user interface level", opt)
	}
	c.UI, c.EndDialog = ui, plus
	return nil
}

func validPropertyName(n string) bool {
	if n == "" {
		return false
	}
	for i, r := range n {
		switch {
		case r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z':
		case i > 0 && (r >= '0' && r <= '9' || r == '.'):
		default:
			return false
		}
	}
	return true
}

// property applies one PROPERTY=value (docs/installer-cli.md §3).
func (c *Command) property(name, value string) *Error {
	c.Props[name] = value
	first := ""
	if value != "" {
		first = strings.ToUpper(value[:1])
	}
	switch name {
	case "REBOOT":
		switch first {
		case "F":
			return c.setReboot(RebootForce, "REBOOT="+value)
		case "S", "R":
			return c.setReboot(RebootSuppress, "REBOOT="+value)
		}
		c.Ignored = append(c.Ignored, name) // only the first character counts; others have no effect
	case "REBOOTPROMPT":
		switch {
		case first == "S":
			c.Prompt = PromptSuppress
		case value == "":
			c.Prompt = PromptAlways
		default:
			c.Ignored = append(c.Ignored, name)
		}
	case "ALLUSERS":
		switch value {
		case "1", "2":
		case "":
			return refuse(InvalidCommandLine, "ALLUSERS=\"\" asks for a per-user installation; this installer installs per machine only")
		default:
			return refuse(InvalidCommandLine, "ALLUSERS=%s: only 1 and 2 are accepted (this installer installs per machine)", value)
		}
	case "MSIINSTALLPERUSER":
		// Only read with ALLUSERS=2 (checked at the end of Parse's caller through PerUser).
	case "ENABLE", "FORCE", "SIGNIN":
		if value != "0" && value != "1" {
			return refuse(InvalidCommandLine, "%s=%s: the value is 0 or 1", name, value)
		}
		switch name {
		case "ENABLE":
			c.Enable, c.EnableSet = value == "1", true
		case "SIGNIN":
			c.SignIn, c.SignInSet = value == "1", true
		default:
			c.Force = value == "1"
		}
	case "REINSTALL":
		if !strings.EqualFold(value, "ALL") {
			return refuse(InvalidCommandLine, "REINSTALL=%s: this installer has one feature; only REINSTALL=ALL is accepted", value)
		}
		return c.propertyAction(Repair, "REINSTALL=ALL")
	case "REINSTALLMODE":
		if err := checkLetters(value, repairLetters, "REINSTALLMODE"); err != nil {
			return err
		}
		c.RepairMode = strings.ToLower(value)
	case "REMOVE":
		if !strings.EqualFold(value, "ALL") {
			return refuse(InvalidCommandLine, "REMOVE=%s: this installer has one feature; only REMOVE=ALL is accepted", value)
		}
		return c.propertyAction(Uninstall, "REMOVE=ALL")
	case "ADDLOCAL":
		if !strings.EqualFold(value, "ALL") {
			return refuse(InvalidCommandLine, "ADDLOCAL=%s: this installer has one feature; only ADDLOCAL=ALL is accepted", value)
		}
		c.Ignored = append(c.Ignored, name)
	case "ADDSOURCE", "ADDDEFAULT", "ADVERTISE":
		return refuse(InvalidCommandLine, "%s: this installer has no source or advertised features", name)
	case "TRANSFORMS":
		if value != "" {
			return refuse(TransformFailure, "TRANSFORMS: this installer takes no transforms")
		}
	case "PATCH", "MSIPATCHREMOVE":
		if value != "" {
			return refuse(PatchOpenFailed, "%s: this installer takes no patches", name)
		}
	case "TARGETDIR", "INSTALLDIR", "INSTALLLOCATION", "APPLICATIONFOLDER":
		return refuse(InvalidCommandLine, "%s: this installer installs into a fixed place", name)
	default:
		// MSIRESTARTMANAGERCONTROL, MSIDISABLERMRESTART, MSIRMSHUTDOWN and every property this installer does
		// not know: accepted and ignored, as Windows Installer sets a property no table reads.
		c.Ignored = append(c.Ignored, name)
	}
	return nil
}

// finish checks what needs the whole command line: MSIINSTALLPERUSER=1 with ALLUSERS=2 asks for per-user.
func (c *Command) finish() *Error {
	if c.Props["ALLUSERS"] == "2" && c.Props["MSIINSTALLPERUSER"] == "1" {
		return refuse(InvalidCommandLine, "ALLUSERS=2 with MSIINSTALLPERUSER=1 asks for a per-user installation; this installer installs per machine only")
	}
	return nil
}
