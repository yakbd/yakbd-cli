import Foundation

// The layout file of the check keyboard (layout.json in the extension's bundle). It is a small format of this
// check only, not the touch runtime file of the yakbd iOS target.
struct CheckKey: Decodable {
    let id: String
    let label: String?
    let output: String?
    // "backspace", "space" or "globe"; nil for a key that types its output.
    let special: String?
    let longPress: [String]?
}

struct CheckLayout: Decodable {
    let name: String
    let rows: [[CheckKey]]
    // Dead key text -> (next key's text -> composed text).
    let deadKeys: [String: [String: String]]?

    static func load(from bundle: Bundle) -> CheckLayout? {
        guard let url = bundle.url(forResource: "layout", withExtension: "json"),
              let data = try? Data(contentsOf: url)
        else { return nil }
        return try? JSONDecoder().decode(CheckLayout.self, from: data)
    }
}
