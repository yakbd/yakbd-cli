// Package loss records what a conversion or build dropped, changed or refused.
//
// Every reader, writer, converter and compiler of yakbd returns a *Report next to its result. A report is a flat
// list of entries; each entry has a severity, a stable code, a location and a message written for people. The
// report never decides on its own whether a loss is acceptable: a caller (or, later, a policy written in the
// layout file) can raise or lower the severity of an entry by its code.
package loss

import (
	"fmt"
	"sort"
	"strings"
)

// Severity says how serious an entry is.
type Severity int

const (
	// Info records a change that keeps the meaning (a normalised spelling, an added default).
	Info Severity = iota
	// Accepted records a loss that a policy declared acceptable.
	Accepted
	// Warning records a loss or a doubtful input that did not stop the operation.
	Warning
	// Error records something that makes the result wrong or impossible; the operation fails.
	Error
)

// String returns the lower-case name of the severity.
func (s Severity) String() string {
	switch s {
	case Info:
		return "info"
	case Accepted:
		return "accept"
	case Warning:
		return "warning"
	case Error:
		return "error"
	}
	return fmt.Sprintf("severity(%d)", int(s))
}

// Class says what an entry records.
type Class int

const (
	// Diagnostic is a remark about the input (a malformed line, a doubtful value).
	Diagnostic Class = iota
	// Loss records data that a conversion or build dropped or changed.
	Loss
	// Defaulted records a choice the tool took from a default rule because the input did not state it. The
	// default rules are deliberately simple and often wrong; a file should carry the real choice. The entry's
	// code is the id of the default rule.
	Defaulted
)

// String returns the lower-case name of the kind.
func (k Class) String() string {
	switch k {
	case Diagnostic:
		return "diagnostic"
	case Loss:
		return "loss"
	case Defaulted:
		return "defaulted"
	}
	return fmt.Sprintf("class(%d)", int(k))
}

// Where locates an entry. All fields are optional.
type Where struct {
	File string `json:"file,omitempty"` // file name as given by the caller
	Line int    `json:"line,omitempty"` // 1-based line number, 0 if not known
	Path string `json:"path,omitempty"` // location inside a structure, such as "keys[3].output"
}

// String renders the location as file:line: path, leaving out empty parts.
func (w Where) String() string {
	var parts []string
	if w.File != "" {
		if w.Line > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", w.File, w.Line))
		} else {
			parts = append(parts, w.File)
		}
	} else if w.Line > 0 {
		parts = append(parts, fmt.Sprintf("line %d", w.Line))
	}
	if w.Path != "" {
		parts = append(parts, w.Path)
	}
	return strings.Join(parts, ": ")
}

// Entry is one item of a report.
type Entry struct {
	Class    Class          `json:"class"`
	Severity Severity       `json:"severity"`
	Code     string         `json:"kind"` // stable identifier, such as "klc.vk.unknown"; policies refer to it
	Where    Where          `json:"where"`
	Message  string         `json:"message"`
	Detail   map[string]any `json:"detail,omitempty"` // free-form; for defaulted entries "value" holds the chosen value
}

// MarshalText encodes the severity by name.
func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// MarshalText encodes the class by name.
func (k Class) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

// String renders the entry on one line.
func (e Entry) String() string {
	w := e.Where.String()
	if w != "" {
		w += ": "
	}
	sev := e.Severity.String()
	if e.Class != Diagnostic {
		sev += " (" + e.Class.String() + ")"
	}
	return fmt.Sprintf("%s%s: %s [%s]", w, sev, e.Message, e.Code)
}

// Report is the list of entries of one operation. The zero value is ready to use.
type Report struct {
	Entries []Entry `json:"entries"`
}

// Add appends an entry.
func (r *Report) Add(sev Severity, code string, where Where, format string, args ...any) {
	r.Entries = append(r.Entries, Entry{Severity: sev, Code: code, Where: where, Message: fmt.Sprintf(format, args...)})
}

// Lost appends a Loss entry with the given severity.
func (r *Report) Lost(sev Severity, code string, where Where, format string, args ...any) {
	r.Entries = append(r.Entries, Entry{Class: Loss, Severity: sev, Code: code, Where: where, Message: fmt.Sprintf(format, args...)})
}

// Defaulted appends a Defaulted entry (severity Info): the tool used default rule id for something the input did
// not state.
func (r *Report) Defaulted(id string, where Where, format string, args ...any) {
	r.Entries = append(r.Entries, Entry{Class: Defaulted, Severity: Info, Code: id, Where: where, Message: fmt.Sprintf(format, args...)})
}

// Errorf appends an Error entry.
func (r *Report) Errorf(code string, where Where, format string, args ...any) {
	r.Add(Error, code, where, format, args...)
}

// Warnf appends a Warning entry.
func (r *Report) Warnf(code string, where Where, format string, args ...any) {
	r.Add(Warning, code, where, format, args...)
}

// Infof appends an Info entry.
func (r *Report) Infof(code string, where Where, format string, args ...any) {
	r.Add(Info, code, where, format, args...)
}

// Merge appends the entries of other.
func (r *Report) Merge(other *Report) {
	if other != nil {
		r.Entries = append(r.Entries, other.Entries...)
	}
}

// HasErrors reports whether any entry has severity Error.
func (r *Report) HasErrors() bool { return r.Count(Error) > 0 }

// Count returns the number of entries with the given severity.
func (r *Report) Count(sev Severity) int {
	n := 0
	for _, e := range r.Entries {
		if e.Severity == sev {
			n++
		}
	}
	return n
}

// Codes returns the distinct codes of the report, sorted.
func (r *Report) Codes() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range r.Entries {
		if !seen[e.Code] {
			seen[e.Code] = true
			out = append(out, e.Code)
		}
	}
	sort.Strings(out)
	return out
}

// Apply changes the severity of every entry whose code is a key of policy. It lets a caller declare a loss an
// error, a warning or accepted.
func (r *Report) Apply(policy map[string]Severity) {
	for i := range r.Entries {
		if s, ok := policy[r.Entries[i].Code]; ok {
			r.Entries[i].Severity = s
		}
	}
}

// ApplyClass sets the severity of every entry of the given kind. ApplyClass(Defaulted, Error) is the policy
// "using a default is an error".
func (r *Report) ApplyClass(class Class, sev Severity) {
	for i := range r.Entries {
		if r.Entries[i].Class == class {
			r.Entries[i].Severity = sev
		}
	}
}

// CountClass returns the number of entries of the given kind.
func (r *Report) CountClass(class Class) int {
	n := 0
	for _, e := range r.Entries {
		if e.Class == class {
			n++
		}
	}
	return n
}

// Err returns an error that lists the Error entries, or nil when there are none.
func (r *Report) Err() error {
	if !r.HasErrors() {
		return nil
	}
	var lines []string
	for _, e := range r.Entries {
		if e.Severity == Error {
			lines = append(lines, e.String())
		}
	}
	return fmt.Errorf("%d error(s):\n%s", len(lines), strings.Join(lines, "\n"))
}

// Summary returns a human summary: one line of counts, then one line per entry, most severe first, in the
// order they were added within one severity.
func (r *Report) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d error(s), %d warning(s), %d accepted, %d info; %d loss(es), %d default(s) used\n",
		r.Count(Error), r.Count(Warning), r.Count(Accepted), r.Count(Info), r.CountClass(Loss), r.CountClass(Defaulted))
	sorted := make([]Entry, len(r.Entries))
	copy(sorted, r.Entries)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Severity > sorted[j].Severity })
	for _, e := range sorted {
		b.WriteString(e.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// Append adds an entry as it is.
func (r *Report) Append(e Entry) { r.Entries = append(r.Entries, e) }
