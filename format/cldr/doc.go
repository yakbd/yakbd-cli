// Package cldr reads and writes Unicode CLDR Keyboard 3.0 XML files (UTS #35 Part 7,
// https://www.unicode.org/reports/tr35/tr35-keyboards.html) and CLDR keyboard test files, into and out of the
// model package, following the mapping of kbl-spec coverage/cldr-keyboard3.md.
//
// Read inlines CLDR imports (the embedded import/ files, CLDR release 49) and implied forms so that the result is
// self-contained, and turns marker dead keys into deadKeys where coverage §3.6 allows it. Write does the reverse
// and carries what CLDR elements cannot hold in one <special> element of the kbl namespace, which Read restores,
// so that a .kbl.json file survives the trip through CLDR.
package cldr
