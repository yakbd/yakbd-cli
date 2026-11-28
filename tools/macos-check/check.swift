// macos-check: registers, enables and selects one installed keyboard-layout bundle, then checks every case of an
// expectation file twice: with UCKeyTranslate over the layout data the system compiled from the bundle, and by
// sending key codes into a text view of this program and reading the text back.
//
// usage: macos-check CASE BUNDLE EXPECT [METHODS]
//   CASE     name printed in every result line
//   BUNDLE   the bundle as installed in a Keyboard Layouts folder, or -control for the control run (README.md)
//   EXPECT   expectation file (see README.md)
//   METHODS  comma-separated typing methods: cgevent, sysevents (default both); "none" skips typing
//
// Every result is one line on standard output: "PASS|FAIL|SKIP|INFO <case> <check> [: detail]". The program never
// stops at the first failure; it exits 1 when any line is FAIL.
//
// Build on a Mac: swiftc -swift-version 5 -O -o macos-check check.swift

import Carbon
import Cocoa

setvbuf(stdout, nil, _IOLBF, 0)

let args = CommandLine.arguments
if args.count < 4 {
    FileHandle.standardError.write("usage: macos-check CASE BUNDLE EXPECT [METHODS]\n".data(using: .utf8)!)
    exit(2)
}
let caseName = args[1]
let bundlePath = args[2]
let expectPath = args[3]
let methods = (args.count > 4 ? args[4] : "cgevent,sysevents").split(separator: ",").map(String.init)
var failed = false

func emit(_ status: String, _ check: String, _ detail: String = "") {
    if status == "FAIL" { failed = true }
    print("\(status) \(caseName) \(check)" + (detail.isEmpty ? "" : " : \(detail)"))
}

// A hard deadline, so a hung window server or a dialog never holds the job.
Thread.detachNewThread {
    Thread.sleep(forTimeInterval: 300)
    print("FAIL \(caseName) deadline : still running after 300 s")
    exit(3)
}

// Spins the main run loop (and dispatches events once the app exists) for the given time.
var appStarted = false
func spin(_ seconds: Double) {
    let until = Date(timeIntervalSinceNow: seconds)
    while Date() < until {
        if appStarted {
            if let ev = NSApp.nextEvent(matching: .any, until: Date(timeIntervalSinceNow: 0.01), inMode: .default, dequeue: true) {
                NSApp.sendEvent(ev)
            }
        } else {
            RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.01))
        }
    }
}

// Shows a string with its code points, for failure details.
func show(_ s: String) -> String {
    let cps = s.unicodeScalars.map { String(format: "U+%04X", $0.value) }.joined(separator: " ")
    let printable = s.unicodeScalars.map { $0.value < 0x20 || $0.value == 0x7F ? "?" : String($0) }.joined()
    return "\"\(printable)\" [\(cps)]"
}

// ---- the expectation file ----

// rshift and roption hold the right-hand key (and set shift or option too): whether macOS hands a layout the
// right-hand state at all is what the case "sides" measures (targets/macos.md §8 of the kbl spec).
struct Stroke {
    let code: UInt16; let shift: Bool; let option: Bool; let caps: Bool
    var rshift = false; var roption = false
}
struct Case { let name: String; let strokes: [Stroke]; let want: String }

func unescape(_ s: String) -> String {
    var out = ""
    var it = s.unicodeScalars.makeIterator()
    while let c = it.next() {
        guard c == "\\" else { out.unicodeScalars.append(c); continue }
        guard let n = it.next() else { out += "\\"; break }
        switch n {
        case "s": out += " "
        case "t": out += "\t"
        case "\\": out += "\\"
        case "u":
            // \u{HEX}
            var hex = ""
            _ = it.next() // {
            while let h = it.next(), h != "}" { hex.unicodeScalars.append(h) }
            if let v = UInt32(hex, radix: 16), let u = Unicode.Scalar(v) { out.unicodeScalars.append(u) }
        default: out += "\\"; out.unicodeScalars.append(n)
        }
    }
    return out
}

func loadCases() -> [Case] {
    guard let text = try? String(contentsOfFile: expectPath, encoding: .utf8) else {
        emit("FAIL", "expect-file", "cannot read \(expectPath)")
        return []
    }
    var cases: [Case] = []
    for (i, line) in text.components(separatedBy: "\n").enumerated() {
        let t = line.trimmingCharacters(in: .whitespaces)
        if t.isEmpty || t.hasPrefix("#") { continue }
        let f = line.components(separatedBy: "\t")
        guard f.count == 3 else { emit("FAIL", "expect-file", "line \(i + 1): want 3 tab-separated fields"); continue }
        var strokes: [Stroke] = []
        var ok = true
        for tok in f[1].split(separator: " ") {
            let parts = tok.split(separator: "+").map(String.init)
            guard let code = UInt16(parts[0]), code < 128 else { ok = false; break }
            let mods = Set(parts.dropFirst())
            if !mods.isSubset(of: ["shift", "option", "caps", "rshift", "roption"]) { ok = false; break }
            strokes.append(Stroke(code: code, shift: mods.contains("shift") || mods.contains("rshift"),
                                  option: mods.contains("option") || mods.contains("roption"), caps: mods.contains("caps"),
                                  rshift: mods.contains("rshift"), roption: mods.contains("roption")))
        }
        if !ok || strokes.isEmpty { emit("FAIL", "expect-file", "line \(i + 1): bad strokes \"\(f[1])\""); continue }
        cases.append(Case(name: f[0], strokes: strokes, want: unescape(f[2])))
    }
    return cases
}

// ---- input sources ----

func stringProp(_ s: TISInputSource, _ key: CFString) -> String? {
    guard let p = TISGetInputSourceProperty(s, key) else { return nil }
    return Unmanaged<CFString>.fromOpaque(p).takeUnretainedValue() as String
}

func boolProp(_ s: TISInputSource, _ key: CFString) -> Bool {
    guard let p = TISGetInputSourceProperty(s, key) else { return false }
    return CFBooleanGetValue(Unmanaged<CFBoolean>.fromOpaque(p).takeUnretainedValue())
}

func sources(id: String?) -> [TISInputSource] {
    let filter: CFDictionary? = id.map { [kTISPropertyInputSourceID as String: $0] as CFDictionary }
    guard let list = TISCreateInputSourceList(filter, true)?.takeRetainedValue() else { return [] }
    return (list as NSArray).map { $0 as! TISInputSource }
}

func waitFor(_ seconds: Double, _ f: () -> TISInputSource?) -> TISInputSource? {
    let until = Date(timeIntervalSinceNow: seconds)
    repeat {
        if let s = f() { return s }
        spin(0.25)
    } while Date() < until
    return nil
}

func currentID() -> String {
    guard let cur = TISCopyCurrentKeyboardInputSource()?.takeRetainedValue() else { return "(none)" }
    return stringProp(cur, kTISPropertyInputSourceID) ?? "(no id)"
}

let start = Date()
func elapsed() -> String { String(format: "%.1f s", Date().timeIntervalSince(start)) }
let waitSeconds = Double(ProcessInfo.processInfo.environment["MACOS_CHECK_WAIT"] ?? "") ?? 60

func describe(_ s: TISInputSource) -> String {
    "type \(stringProp(s, kTISPropertyInputSourceType) ?? "?"), category \(stringProp(s, kTISPropertyInputSourceCategory) ?? "?"), enable-capable \(boolProp(s, kTISPropertyInputSourceIsEnableCapable)), select-capable \(boolProp(s, kTISPropertyInputSourceIsSelectCapable)), enabled \(boolProp(s, kTISPropertyInputSourceIsEnabled)), selected \(boolProp(s, kTISPropertyInputSourceIsSelected))"
}

// Control mode (BUNDLE "-control"): the same enable, select and typing steps on one of Apple's own layouts that is
// installed but not enabled, with the expectations taken from its own UCKeyTranslate output. It tells a runner
// session where enabling works at all from one where only a freshly installed bundle fails.
let control = bundlePath == "-control"
var layout: (name: String, id: String) = ("", "")
if control {
    let all = sources(id: nil).filter {
        stringProp($0, kTISPropertyInputSourceType) == (kTISTypeKeyboardLayout as String)
            && (stringProp($0, kTISPropertyInputSourceID) ?? "").hasPrefix("com.apple.keylayout.")
            && !boolProp($0, kTISPropertyInputSourceIsEnabled)
    }
    let pick = all.first { stringProp($0, kTISPropertyInputSourceID) == "com.apple.keylayout.German" } ?? all.first
    guard let p = pick else {
        emit("FAIL", "control", "no installed, disabled Apple keyboard layout found")
        exit(1)
    }
    layout = (stringProp(p, kTISPropertyLocalizedName) ?? "?", stringProp(p, kTISPropertyInputSourceID) ?? "?")
    emit("INFO", "control", "\(layout.id) (\(layout.name)), \(all.count) disabled Apple layouts; \(describe(p))")
} else {
    // The layouts the bundle declares: KLInfo_<name> entries of its Info.plist.
    let plistURL = URL(fileURLWithPath: bundlePath).appendingPathComponent("Contents/Info.plist")
    var declared: [(name: String, id: String)] = []
    if let plist = NSDictionary(contentsOf: plistURL) as? [String: Any] {
        for (k, v) in plist where k.hasPrefix("KLInfo_") {
            if let d = v as? [String: Any], let id = d["TISInputSourceID"] as? String {
                declared.append((String(k.dropFirst("KLInfo_".count)), id))
            }
        }
    }
    guard let first = declared.first else {
        emit("FAIL", "info-plist", "no KLInfo_<name> entry with a TISInputSourceID in \(plistURL.path)")
        exit(1)
    }
    layout = first
    emit("INFO", "declared", "layout \(layout.name), source id \(layout.id)")
}
emit("INFO", "current-before", currentID())

// Three lookups, each with the time it first saw the source: the ID filter, a scan of the whole list by ID, and a
// scan by name. The ID filter missed a registered layout in the first runs; find() takes the first that answers.
var seen: [String: String] = [:]
func find() -> TISInputSource? {
    var hit: TISInputSource?
    if let s = sources(id: layout.id).first { hit = s; if seen["filter"] == nil { seen["filter"] = elapsed() } }
    let all = sources(id: nil)
    if let s = all.first(where: { stringProp($0, kTISPropertyInputSourceID) == layout.id }) {
        hit = hit ?? s; if seen["scan-id"] == nil { seen["scan-id"] = elapsed() }
    }
    if let s = all.first(where: { stringProp($0, kTISPropertyLocalizedName) == layout.name }) {
        hit = hit ?? s; if seen["scan-name"] == nil { seen["scan-name"] = elapsed() }
    }
    return hit
}
func seenText() -> String {
    ["filter", "scan-id", "scan-name"].map { "\($0) \(seen[$0] ?? "never")" }.joined(separator: ", ")
}

var source: TISInputSource?
if control {
    source = find()
} else {
    // Is the source known after the copy alone, before any registration?
    if waitFor(5, find) != nil {
        let ids = sources(id: nil).filter { stringProp($0, kTISPropertyLocalizedName) == layout.name }.map { stringProp($0, kTISPropertyInputSourceID) ?? "?" }
        emit("INFO", "visible-after-copy", "known without TISRegisterInputSource (\(seenText())); listed by name with id \(ids.joined(separator: ", "))")
    } else {
        emit("INFO", "visible-after-copy", "not known after 5 s without registration")
    }
    let reg = TISRegisterInputSource(URL(fileURLWithPath: bundlePath) as CFURL)
    emit(reg == noErr ? "PASS" : "FAIL", "register", "TISRegisterInputSource = \(reg) at \(elapsed())")
    source = waitFor(waitSeconds, find)
    // Let every lookup have its chance before reporting.
    if source != nil { for _ in 0..<8 { _ = find(); spin(0.25) } }
    if let s = source {
        let found = stringProp(s, kTISPropertyInputSourceID) ?? "?"
        emit(found == layout.id ? "PASS" : "FAIL", "visible", "source \(found); first seen: \(seenText())")
    } else {
        emit("FAIL", "visible", "not listed \(Int(waitSeconds)) s after registration; first seen: \(seenText())")
    }
}
if let s = source {
    emit("INFO", "properties", describe(s))
    // MACOS_CHECK_NAME (the case's name.txt): the name the system shows for the layout, from the bundle's
    // <tag>.lproj/InfoPlist.strings for the session's languages.
    if let want = ProcessInfo.processInfo.environment["MACOS_CHECK_NAME"], !want.isEmpty {
        let got = stringProp(s, kTISPropertyLocalizedName) ?? "(none)"
        emit(got == want ? "PASS" : "FAIL", "localized-name", got == want ? got : "got \(show(got)) want \(show(want))")
    }
}
if ProcessInfo.processInfo.environment["MACOS_CHECK_REGISTER_ONLY"] == "1" { exit(failed ? 1 : 0) }

// Calls TISEnableInputSource every 2 s until a freshly listed object says it is enabled.
func enableAndWait(_ seconds: Double) -> (calls: Int, last: OSStatus, enabled: Bool) {
    var calls = 0
    var last: OSStatus = noErr
    var lastCall = Date.distantPast
    let until = Date(timeIntervalSinceNow: seconds)
    repeat {
        if let s = find() {
            if boolProp(s, kTISPropertyInputSourceIsEnabled) { return (calls, last, true) }
            if Date().timeIntervalSince(lastCall) >= 2 {
                last = TISEnableInputSource(s); calls += 1; lastCall = Date()
            }
        }
        spin(0.25)
    } while Date() < until
    return (calls, last, find().map { boolProp($0, kTISPropertyInputSourceIsEnabled) } ?? false)
}

var uchr: Data?
var selected = false
if let s0 = source {
    let e = enableAndWait(15)
    emit(e.enabled ? "PASS" : "FAIL", "enable", "\(e.calls) TISEnableInputSource calls, last = \(e.last), enabled = \(e.enabled) at \(elapsed())")
    var sel: OSStatus = -1
    var selCalls = 0
    let selUntil = Date(timeIntervalSinceNow: 10)
    repeat {
        if let s = find() { sel = TISSelectInputSource(s); selCalls += 1 }
        if sel == noErr { break }
        spin(1)
    } while Date() < selUntil
    emit(sel == noErr ? "PASS" : "FAIL", "select", "\(selCalls) TISSelectInputSource calls, last = \(sel)")
    let s = find() ?? s0
    let want = stringProp(s, kTISPropertyInputSourceID) ?? layout.id
    var cur = currentID()
    let until = Date(timeIntervalSinceNow: 5)
    while cur != want && Date() < until { spin(0.25); cur = currentID() }
    selected = cur == want
    emit(selected ? "PASS" : "FAIL", "current", "current keyboard input source \(cur)")
    if let p = TISGetInputSourceProperty(s, kTISPropertyUnicodeKeyLayoutData) {
        uchr = Unmanaged<CFData>.fromOpaque(p).takeUnretainedValue() as Data
        emit("PASS", "uchr", "system compiled the layout: \(uchr!.count) bytes")
    } else {
        emit("FAIL", "uchr", "no kTISPropertyUnicodeKeyLayoutData: the XML did not compile")
    }
}

// ---- (a) UCKeyTranslate ----

func modState(_ k: Stroke) -> UInt32 {
    // Carbon modifier bits shifted right by 8: shift 0x02, caps lock 0x04, option 0x08.
    var m: UInt32 = (k.shift ? 0x02 : 0) | (k.caps ? 0x04 : 0) | (k.option ? 0x08 : 0)
    // Right-hand keys add rightShiftKey / rightOptionKey (Carbon Events.h) to the either-side bit.
    if k.rshift { m |= UInt32(rightShiftKey) >> 8 }
    if k.roption { m |= UInt32(rightOptionKey) >> 8 }
    return m
}

// Translates a stroke sequence, carrying the dead-key state; returns the text, the status and the state left.
func translate(_ data: Data, _ strokes: [Stroke]) -> (String, OSStatus, UInt32) {
    let kbdType = UInt32(LMGetKbdType())
    return data.withUnsafeBytes { (raw: UnsafeRawBufferPointer) -> (String, OSStatus, UInt32) in
        let ptr = raw.baseAddress!.assumingMemoryBound(to: UCKeyboardLayout.self)
        var dead: UInt32 = 0
        var got = ""
        for k in strokes {
            var buf = [UniChar](repeating: 0, count: 16)
            var n = 0
            let err = UCKeyTranslate(ptr, k.code, UInt16(kUCKeyActionDown), modState(k), kbdType, 0, &dead, buf.count, &n, &buf)
            if err != noErr { return (got, err, dead) }
            got += String(utf16CodeUnits: buf, count: n)
        }
        return (got, noErr, dead)
    }
}

var cases: [Case] = []
if control {
    // The control's expectations are what its own layout data says, so nothing of Apple's is written down here.
    if let data = uchr {
        for code: UInt16 in [6, 16, 12, 41] {
            let k = [Stroke(code: code, shift: false, option: false, caps: false)]
            cases.append(Case(name: "key-\(code)", strokes: k, want: translate(data, k).0))
        }
        emit("INFO", "control-expect", cases.map { "\($0.name) \(show($0.want))" }.joined(separator: ", "))
    }
} else {
    cases = loadCases()
    if let data = uchr {
        for c in cases {
            let (got, err, dead) = translate(data, c.strokes)
            if c.strokes.contains(where: { $0.rshift || $0.roption }) {
                // Which bits UCKeyTranslate wants for a right-hand key is not documented: shown, not checked.
                emit("INFO", "translate:\(c.name)", "status \(err), got \(show(got)) (right-hand bits are this checker's guess)")
                continue
            }
            if err != noErr {
                emit("FAIL", "translate:\(c.name)", "UCKeyTranslate = \(err)")
            } else if got == c.want {
                emit("PASS", "translate:\(c.name)")
            } else {
                emit("FAIL", "translate:\(c.name)", "got \(show(got)) want \(show(c.want))" + (dead != 0 ? " (dead-key state \(dead) left)" : ""))
            }
        }
    } else {
        for c in cases { emit("SKIP", "translate:\(c.name)", "no layout data") }
    }
}

// ---- (b) typing key codes into a text view ----

let typing = methods.filter { $0 != "none" }
if !typing.isEmpty && !selected {
    for m in typing { for c in cases { emit("SKIP", "type-\(m):\(c.name)", "the layout is not the current input source") } }
} else if !typing.isEmpty {
    let app = NSApplication.shared
    app.setActivationPolicy(.regular)
    app.finishLaunching()
    appStarted = true
    let window = NSWindow(contentRect: NSRect(x: 200, y: 200, width: 480, height: 200),
                          styleMask: [.titled], backing: .buffered, defer: false)
    window.title = "macos-check \(caseName)"
    let scroll = NSScrollView(frame: window.contentView!.bounds)
    let text = NSTextView(frame: scroll.bounds)
    text.isRichText = false
    text.isAutomaticQuoteSubstitutionEnabled = false
    text.isAutomaticDashSubstitutionEnabled = false
    text.isAutomaticTextReplacementEnabled = false
    text.isAutomaticSpellingCorrectionEnabled = false
    text.isContinuousSpellCheckingEnabled = false
    scroll.documentView = text
    window.contentView = scroll
    window.makeKeyAndOrderFront(nil)
    window.makeFirstResponder(text)
    app.activate(ignoringOtherApps: true)
    spin(1)
    if NSWorkspace.shared.frontmostApplication?.processIdentifier != getpid() {
        // Fall back to System Events, which the runner image may allow to raise a process.
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
        p.arguments = ["-e", "tell application \"System Events\" to set frontmost of (first process whose unix id is \(getpid())) to true"]
        try? p.run(); p.waitUntilExit()
        spin(1)
    }
    let front = NSWorkspace.shared.frontmostApplication
    emit(front?.processIdentifier == getpid() ? "PASS" : "FAIL", "frontmost",
         "frontmost app \(front?.localizedName ?? "?") pid \(front?.processIdentifier ?? -1), ours \(getpid()), key window \(window.isKeyWindow)")
    emit("INFO", "permissions", "AXIsProcessTrusted \(AXIsProcessTrusted()), CGPreflightPostEventAccess \(CGPreflightPostEventAccess())")
    let wantID = layout.id
    let before = currentID()
    emit(before == wantID ? "PASS" : "FAIL", "current-before-typing", before)

    func post(_ k: Stroke) {
        var flags: CGEventFlags = []
        if k.shift { flags.insert(.maskShift) }
        if k.option { flags.insert(.maskAlternate) }
        if k.caps { flags.insert(.maskAlphaShift) }
        let src = CGEventSource(stateID: .hidSystemState)
        // A right-hand modifier: its device bit (IOLLEvent.h: NX_DEVICERSHIFTKEYMASK 0x04, NX_DEVICERALTKEYMASK
        // 0x40) on the key events, and the modifier key itself (kVK_RightShift, kVK_RightOption) pressed around them.
        var side: [UInt16] = []
        if k.rshift { flags.insert(CGEventFlags(rawValue: 0x04)); side.append(UInt16(kVK_RightShift)) }
        if k.roption { flags.insert(CGEventFlags(rawValue: 0x40)); side.append(UInt16(kVK_RightOption)) }
        for m in side {
            if let e = CGEvent(keyboardEventSource: src, virtualKey: m, keyDown: true) {
                e.type = .flagsChanged; e.flags = flags; e.post(tap: .cghidEventTap)
            }
            spin(0.03)
        }
        defer {
            for m in side.reversed() {
                if let e = CGEvent(keyboardEventSource: src, virtualKey: m, keyDown: false) {
                    e.type = .flagsChanged; e.flags = []; e.post(tap: .cghidEventTap)
                }
                spin(0.03)
            }
        }
        for down in [true, false] {
            if let e = CGEvent(keyboardEventSource: src, virtualKey: k.code, keyDown: down) {
                e.flags = flags
                e.post(tap: .cghidEventTap)
            }
            spin(0.03)
        }
    }

    func systemEvents(_ k: Stroke) -> String? {
        var using: [String] = []
        if k.shift { using.append("shift down") }
        if k.option { using.append("option down") }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
        let tail = using.isEmpty ? "" : " using {\(using.joined(separator: ", "))}"
        p.arguments = ["-e", "tell application \"System Events\" to key code \(k.code)\(tail)"]
        let errPipe = Pipe()
        p.standardError = errPipe
        do { try p.run() } catch { return "osascript: \(error)" }
        while p.isRunning { spin(0.02) }
        if p.terminationStatus != 0 {
            let msg = String(data: errPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
            return "osascript exit \(p.terminationStatus): \(msg.trimmingCharacters(in: .whitespacesAndNewlines))"
        }
        return nil
    }

    for m in typing {
        guard m == "cgevent" || m == "sysevents" else { emit("FAIL", "type-\(m)", "unknown method"); continue }
        for c in cases {
            let check = "type-\(m):\(c.name)"
            if m == "sysevents" && c.strokes.contains(where: { $0.caps }) {
                emit("SKIP", check, "System Events key code has no caps-lock modifier")
                continue
            }
            if m == "sysevents" && c.strokes.contains(where: { $0.rshift || $0.roption }) {
                emit("SKIP", check, "System Events key code has no right-hand modifiers")
                continue
            }
            text.string = ""
            window.makeFirstResponder(text)
            spin(0.1)
            var err: String?
            for k in c.strokes {
                if m == "cgevent" { post(k) } else if let e = systemEvents(k) { err = e; break }
                spin(0.08)
            }
            spin(0.4)
            let got = text.string
            if let e = err {
                emit("FAIL", check, e)
            } else if got == c.want {
                emit("PASS", check)
            } else {
                emit("FAIL", check, "got \(show(got)) want \(show(c.want))")
            }
        }
    }
    let after = currentID()
    emit(after == wantID ? "PASS" : "FAIL", "current-after-typing", after)
    window.orderOut(nil)
}

if control, let s = find() {
    // Leave the runner as it was: US selected, the control layout disabled again.
    if let us = sources(id: "com.apple.keylayout.US").first { _ = TISSelectInputSource(us) }
    _ = TISDisableInputSource(s)
}

exit(failed ? 1 : 0)
