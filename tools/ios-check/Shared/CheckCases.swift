import Foundation

// One line of expect.tsv: name, steps separated by spaces, expected text. A step is a key id (tap it) or
// "long:<key id>=<choice id>" (hold the key, then tap the choice). In the expected text "\s" is a space,
// "\\" a backslash and "\u{HEX}" any code point. Lines starting with "#" are comments.
struct CheckCase {
    let name: String
    let steps: [String]
    let expected: String
}

enum CheckCases {
    struct Failure: Error, CustomStringConvertible {
        let description: String
    }

    static func load(from bundle: Bundle) throws -> [CheckCase] {
        guard let url = bundle.url(forResource: "expect", withExtension: "tsv") else {
            throw Failure(description: "expect.tsv is not in the test bundle")
        }
        let text = try String(contentsOf: url, encoding: .utf8)
        var cases: [CheckCase] = []
        for line in text.split(separator: "\n") where !line.hasPrefix("#") {
            let fields = line.split(separator: "\t", omittingEmptySubsequences: false)
            guard fields.count == 3 else {
                throw Failure(description: "expect.tsv: not three fields: \(line)")
            }
            cases.append(CheckCase(
                name: String(fields[0]),
                steps: fields[1].split(separator: " ").map(String.init),
                expected: unescape(String(fields[2]))))
        }
        return cases
    }

    static func unescape(_ text: String) -> String {
        var out = ""
        var rest = Substring(text)
        while let first = rest.first {
            rest = rest.dropFirst()
            guard first == "\\", let next = rest.first else {
                out.append(first)
                continue
            }
            rest = rest.dropFirst()
            switch next {
            case "s":
                out.append(" ")
            case "\\":
                out.append("\\")
            case "u":
                if rest.hasPrefix("{"), let close = rest.firstIndex(of: "}"),
                   let value = UInt32(rest[rest.index(after: rest.startIndex)..<close], radix: 16),
                   let scalar = Unicode.Scalar(value) {
                    out.unicodeScalars.append(scalar)
                    rest = rest[rest.index(after: close)...]
                } else {
                    out.append("\\u")
                }
            default:
                out.append("\\")
                out.append(next)
            }
        }
        return out
    }

    // "U+00E6 U+0062" for messages, so that invisible differences show.
    static func codePoints(_ text: String) -> String {
        if text.isEmpty { return "(empty)" }
        return text.unicodeScalars.map { String(format: "U+%04X", $0.value) }.joined(separator: " ")
    }

    // The marker line the driver collects into summary.txt.
    static func report(_ pass: Bool, _ check: String, _ detail: String) {
        print("YAKCASE \(pass ? "PASS" : "FAIL") \(check) : \(detail)")
        fflush(stdout)
    }
}
